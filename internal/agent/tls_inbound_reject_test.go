package agent

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/protobuf/proto"
)

type inboundResponseCapture struct {
	net.Conn
	response bytes.Buffer
}

func (c *inboundResponseCapture) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.response.Write(p[:n])
	return n, err
}

func assertInboundRejectAlert(t *testing.T, address, name string, version uint16) {
	t.Helper()
	raw, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(3 * time.Second))
	capture := &inboundResponseCapture{Conn: raw}
	client := tls.Client(capture, &tls.Config{ServerName: name, InsecureSkipVerify: true, MinVersion: version, MaxVersion: version})
	if err := client.Handshake(); err == nil || !strings.Contains(err.Error(), "unrecognized name") {
		t.Fatalf("expected SNI rejection alert, got %v", err)
	}
	if got := capture.response.Bytes(); !bytes.Equal(got, []byte{21, 3, 3, 0, 2, 2, 112}) {
		t.Fatalf("unexpected rejection response: %x", got)
	}
}

func TestTLSInboundNginxRejectAndDefaultIsolation(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, config, address, clientConfig := inboundFixture(t, root, target, target)
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, name := range []string{"", "unknown.example.test", "outside.invalid"} {
			t.Run(fmt.Sprintf("%x/%s", version, name), func(t *testing.T) {
				assertInboundRejectAlert(t, address, name, version)
			})
		}
	}
	if engine.counters[1].Load() != 0 || engine.counters[2].Load() != 0 {
		t.Fatal("rejected probes reached a tenant route")
	}
	config = proto.Clone(config).(*pb.ControlMessage)
	config.Revision++
	config.Node.TlsInbounds[0].DefaultRuleId = 2
	config.Rules[0].Enabled = false
	if err := engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	// A paused claimed SNI never falls through to a different user's default.
	assertInboundRejectAlert(t, address, "a.example.test", tls.VersionTLS13)
	// A configured default still serves unclaimed names through real target TLS.
	fallbackConfig := clientConfig.Clone()
	fallbackConfig.ServerName = "unknown.example.test"
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, fallbackConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	inboundExchange(t, conn, "default-remains-end-to-end")
}

func TestTLSInboundPlainHTTPBannerIsBoundedAndSeparateFromTLS(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, _, address, clientConfig := inboundFixture(t, root, target)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			raw, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(3 * time.Second))
			// Request data must never be reflected in the generated error page.
			fmt.Fprintf(raw, "%s /private-probe-token HTTP/1.1\r\nHost: private-host.example.test\r\nContent-Length: 0\r\n\r\n", method)
			response, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: method})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != 400 || response.Header.Get("Server") != "nginx" || !response.Close {
				t.Fatal("invalid plaintext rejection", response.Status, response.Header, err)
			}
			if _, err := http.ParseTime(response.Header.Get("Date")); err != nil {
				t.Fatal(err)
			}
			if response.ContentLength != int64(len(plainHTTPOnTLSBody)) || strings.Contains(string(body), "private-") {
				t.Fatal("incorrect content length or reflected request")
			}
			if method == http.MethodHead {
				if len(body) != 0 {
					t.Fatal("HEAD response contains a body")
				}
			} else if string(body) != plainHTTPOnTLSBody {
				t.Fatal("incorrect plaintext error page")
			}
		})
	}
	for _, request := range []string{"GET / HTTP/1.1\r\nHost: example.test\r\nX-Pad: " + strings.Repeat("x", 17*1024) + "\r\n\r\n", string([]byte{23, 3, 3, 0, 1, 0})} {
		raw, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		raw.SetDeadline(time.Now().Add(3 * time.Second))
		io.WriteString(raw, request)
		body, _ := io.ReadAll(raw)
		raw.Close()
		if len(body) != 0 {
			t.Fatalf("malformed or oversized probe received a banner: %q", body)
		}
	}
	if engine.counters[1].Load() != 0 {
		t.Fatal("plaintext probes were charged to a user")
	}
	// Peeking must not consume TLS bytes or replace the target certificate.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	inboundExchange(t, conn, "still-transparent")
}
