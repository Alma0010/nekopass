package agent

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	for _, request := range []string{string([]byte{23, 3, 3, 0, 1, 0}), "GET / HTTP/1.1\r\nHost: unfinished.example.test"} {
		raw, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		raw.SetDeadline(time.Now().Add(3 * time.Second))
		io.WriteString(raw, request)
		// End an incomplete request without waiting for the handshake timeout.
		raw.(*net.TCPConn).CloseWrite()
		body, _ := io.ReadAll(raw)
		raw.Close()
		if len(body) != 0 {
			t.Fatalf("binary or incomplete probe received a banner: %q", body)
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

// These response bodies, status lines and HEAD semantics were checked against
// nginx/1.30.6 with server_tokens off and default HTTP header buffer limits.
func TestTLSInboundPlainHTTPErrorsMatchNginx(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, _, address, _ := inboundFixture(t, root, target)
	for _, tc := range []struct {
		name, request, body string
		status              int
		head                bool
	}{
		{"http10", "GET / HTTP/1.0\r\n\r\n", plainHTTPOnTLSBody, 400, false},
		{"no_host", "GET / HTTP/1.1\r\n\r\n", badHTTPRequestBody, 400, false},
		{"head_no_host", "HEAD / HTTP/1.1\r\n\r\n", badHTTPRequestBody, 400, true},
		{"no_colon", "GET / HTTP/1.1\r\nHost: example.test\r\nNo-Colon\r\n\r\n", plainHTTPOnTLSBody, 400, false},
		{"invalid_header_name", "GET / HTTP/1.1\r\nHost: example.test\r\nBad Name: 1\r\n\r\n", badHTTPRequestBody, 400, false},
		{"underscore_header", "GET / HTTP/1.1\r\nHost: example.test\r\nX_Test: 1\r\n\r\n", plainHTTPOnTLSBody, 400, false},
		{"control_header", "GET / HTTP/1.1\r\nHost: example.test\r\nX-Test: a\x00b\r\n\r\n", badHTTPRequestBody, 400, false},
		{"duplicate_host", "GET / HTTP/1.1\r\nHost: a.test\r\nHost: a.test\r\n\r\n", badHTTPRequestBody, 400, false},
		{"empty_host", "GET / HTTP/1.1\r\nHost: \r\n\r\n", badHTTPRequestBody, 400, false},
		{"invalid_host", "GET / HTTP/1.1\r\nHost: a/b.test\r\n\r\n", badHTTPRequestBody, 400, false},
		{"large_header", "GET / HTTP/1.1\r\nHost: example.test\r\nX-Pad: " + strings.Repeat("x", 17*1024) + "\r\n\r\n", largeHTTPHeaderBody, 400, false},
		{"large_head", "HEAD / HTTP/1.1\r\nHost: example.test\r\nX-Pad: " + strings.Repeat("x", 17*1024) + "\r\n\r\n", largeHTTPHeaderBody, 400, true},
		{"large_uri", "GET /" + strings.Repeat("x", 17*1024) + " HTTP/1.1\r\nHost: example.test\r\n\r\n", largeHTTPURIBody, 414, false},
		{"total_header_limit", "GET / HTTP/1.1\r\nHost: example.test\r\n" + strings.Repeat("X-Test: "+strings.Repeat("x", 1000)+"\r\n", 34) + "\r\n", largeHTTPHeaderBody, 400, false},
		{"multiple_large_headers", "GET / HTTP/1.1\r\nHost: example.test\r\n" + strings.Repeat("X-Test: "+strings.Repeat("x", 6000)+"\r\n", 4) + "\r\n", plainHTTPOnTLSBody, 400, false},
		{"duplicate_length", "POST / HTTP/1.1\r\nHost: a.test\r\nContent-Length: 0\r\nContent-Length: 0\r\n\r\n", badHTTPRequestBody, 400, false},
		{"invalid_length", "POST / HTTP/1.1\r\nHost: a.test\r\nContent-Length: bad\r\n\r\n", badHTTPRequestBody, 400, false},
		{"length_and_encoding", "POST / HTTP/1.1\r\nHost: a.test\r\nContent-Length: 0\r\nTransfer-Encoding: chunked\r\n\r\n", badHTTPRequestBody, 400, false},
		{"chunked", "POST / HTTP/1.1\r\nHost: a.test\r\nTransfer-Encoding: chunked\r\n\r\n", plainHTTPOnTLSBody, 400, false},
		{"http2_preface", "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n", badHTTPRequestBody, 400, false},
		{"http2_request_line", "GET / HTTP/2.0\r\nHost: a.test\r\n\r\n", unsupportedHTTPVersionBody, 505, false},
		{"invalid_method", "G@T / HTTP/1.1\r\nHost: a.test\r\n\r\n", badHTTPRequestBody, 400, false},
		{"lowercase_method", "get / HTTP/1.1\r\nHost: a.test\r\n\r\n", badHTTPRequestBody, 400, false},
		{"invalid_request_line", "GET xxx\r\n\r\n", badHTTPRequestBody, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := io.WriteString(conn, tc.request); err != nil {
				t.Fatal(err)
			}
			method := http.MethodGet
			if tc.head {
				method = http.MethodHead
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status || response.Header.Get("Server") != "nginx" || !response.Close || response.ContentLength != int64(len(tc.body)) {
				t.Fatalf("unexpected response: %s %v", response.Status, response.Header)
			}
			want := tc.body
			if tc.head {
				want = ""
			}
			if string(body) != want {
				t.Fatalf("unexpected body: %q", body)
			}
			// Optional differential check against a real nginx configured with
			// server_tokens off and ssl_reject_handshake on, on a local test port.
			if reference := os.Getenv("NEKOPASS_NGINX_REFERENCE_ADDR"); reference != "" {
				comparePlainHTTPWithNginx(t, reference, tc.request, method, response, body)
			}
		})
	}
	if engine.counters[1].Load() != 0 {
		t.Fatal("HTTP errors reached a tenant route or affected user traffic")
	}
}

func comparePlainHTTPWithNginx(t *testing.T, address, request, method string, got *http.Response, body []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reference, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Body.Close()
	want, err := io.ReadAll(reference.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != reference.Status || got.Proto != reference.Proto || got.ContentLength != reference.ContentLength || got.Close != reference.Close || !bytes.Equal(body, want) {
		t.Fatalf("nginx difference: got %s %d %q; reference %s %d %q", got.Status, got.ContentLength, body, reference.Status, reference.ContentLength, want)
	}
	for _, name := range []string{"Server", "Content-Type", "Connection"} {
		if got.Header.Get(name) != reference.Header.Get(name) {
			t.Fatalf("nginx header %s differs: %q / %q", name, got.Header.Get(name), reference.Header.Get(name))
		}
	}
}

func TestTLSInboundPlainHTTPTimeoutAndLimitRecover(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, config, address, clientConfig := inboundFixture(t, root, target)
	config = proto.Clone(config).(*pb.ControlMessage)
	config.Revision++
	config.Node.TlsInbounds[0].MaxHandshakes = 1
	if err := engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: incomplete")
	if body, _ := io.ReadAll(conn); len(body) != 0 {
		t.Fatalf("incomplete HTTP received a banner: %q", body)
	}
	// A timed-out plaintext request must release its shared handshake slot.
	valid, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer valid.Close()
	inboundExchange(t, valid, "after-http-timeout")
}
