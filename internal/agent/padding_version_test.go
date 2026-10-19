package agent

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nekopass/nekopass/internal/padding"
)

func TestPaddingRejectsOldTCPVersionBeforeBackendDial(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var dials atomic.Int32
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			c.Close()
		}
	}()
	exit, _, cfg, rule := h2Fixture(t, target.Addr().String())
	cfg.Node.TunnelProtocol = "tls_tcp"
	cfg.Node.Tls.PaddingEnabled = true
	if err = exit.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(rule.Tls.RootCa))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := net.Dial("tcp", net.JoinHostPort(rule.TunnelHost, strconv.Itoa(int(rule.TunnelPort))))
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := dialTunnelTLSProtocol(ctx, raw, rule.Tls, roots, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err = c.Write([]byte{'N', 'P', 'T', 2}); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatal("old version accepted", n, err)
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("version mismatch waited for a full request")
	}
	if dials.Load() != 0 {
		t.Fatal("incompatible version reached backend")
	}
}

func TestPaddingRejectsOldUDPRequestVersions(t *testing.T) {
	exit, _, cfg, rule := h2Fixture(t, "127.0.0.1:9")
	cfg.Node.TunnelProtocol = "tls_tcp"
	cfg.Node.Tls.PaddingEnabled = true
	cfg.EgressRules[0].Protocol = "udp"
	if err := exit.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(rule.Tls.RootCa))
	for _, version := range []int{0, 1, padding.WireVersion + 1} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		raw, err := net.Dial("tcp", net.JoinHostPort(rule.TunnelHost, strconv.Itoa(int(rule.TunnelPort))))
		if err != nil {
			t.Fatal(err)
		}
		c, binding, err := dialTunnelTLSProtocol(ctx, raw, rule.Tls, roots, false)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		open := udpStreamOpen{h2Open: h2Open{Network: "udp", Ingress: rule.IngressNodeId, Rule: rule.Id, User: rule.UserId, Epoch: rule.QuotaEpoch, Target: "127.0.0.1:9", Nonce: fmt.Sprintf("%032x", version+1)}, Time: time.Now().Unix(), Padding: true, PaddingVersion: version}
		metadata, _ := json.Marshal(open)
		request, _ := http.NewRequest("POST", "http://tunnel.example.test/api/stream", nil)
		request.Header.Set("X-Stream", base64.RawURLEncoding.EncodeToString(metadata))
		request.Header.Set("Authorization", "Bearer "+h2Proof(rule.TunnelToken, binding, metadata))
		request.Header.Set("X-Stream-Padding", strings.Repeat("0", 30))
		if err = request.Write(c); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(c), request)
		if err != nil {
			t.Fatal("missing version rejection response", version, err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		c.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatal("incompatible UDP padding version accepted", version, response.StatusCode)
		}
	}
}

func TestPaddingClientRejectsOldUDPServerReply(t *testing.T) {
	_, _, cfg, rule := h2Fixture(t, "127.0.0.1:9")
	pair, err := tls.X509KeyPair([]byte(cfg.Node.Tls.Certificate), []byte(cfg.Node.Tls.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	var seenVersion atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata, err := base64.RawURLEncoding.DecodeString(r.Header.Get("X-Stream"))
		if err != nil {
			http.Error(w, "bad metadata", 400)
			return
		}
		var open udpStreamOpen
		json.Unmarshal(metadata, &open)
		seenVersion.Store(int32(open.PaddingVersion))
		binding, err := r.TLS.ExportKeyingMaterial(exporterLabel, nil, 32)
		if err != nil {
			http.Error(w, "bad TLS binding", 400)
			return
		}
		// An old exit can return a valid authentication proof while omitting the
		// framing version. The caller must fail before exposing a datagram conn.
		w.Header().Set("X-Stream-Reply", h2ResponseProof(rule.TunnelToken, binding, metadata))
		w.Header().Set("Content-Length", "0")
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}}
	server.StartTLS()
	defer server.Close()
	rule.TunnelProtocol = "tls_tcp"
	rule.Tls.PaddingEnabled = true
	rule.TunnelHost = "127.0.0.1"
	rule.TunnelPort = int32(server.Listener.Addr().(*net.TCPAddr).Port)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, err := dialRawDatagramTunnel(ctx, rule, "127.0.0.1:9"); err == nil {
		c.Close()
		t.Fatal("old UDP exit reply accepted")
	} else if !strings.Contains(err.Error(), "padding version mismatch") {
		t.Fatal("test did not reach version check", err)
	}
	if seenVersion.Load() != padding.WireVersion {
		t.Fatal("client did not send padding wire version")
	}
}
