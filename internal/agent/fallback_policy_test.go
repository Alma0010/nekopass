package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"time"

	"github.com/nekopass/nekopass/internal/control"
	"net/http/httptest"
	"testing"

	pb "github.com/nekopass/nekopass/internal/protocol"
)

func TestFallbackCookiePolicy(t *testing.T) {
	for _, forward := range []bool{false, true} {
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Cookie") != ""; got != forward {
				t.Errorf("forward=%t request cookie present=%t", forward, got)
			}
			w.Header().Add("Set-Cookie", "session=fixture")
			w.Header().Set("X-Website", "fixture")
			w.WriteHeader(http.StatusOK)
		}))
		e := &Engine{}
		e.node.Store(&pb.NodeConfig{Tls: &pb.TLSServerConfig{FallbackUrl: remote.URL, FallbackForwardCookies: forward}})
		r := httptest.NewRequest(http.MethodGet, "https://public.example.test/", nil)
		r.Header.Set("Cookie", "client=fixture")
		w := httptest.NewRecorder()
		e.camouflage(w, r)
		remote.Close()
		got := w.Result()
		if got.StatusCode != http.StatusOK || got.Header.Get("X-Website") != "fixture" || (len(got.Cookies()) != 0) != forward {
			t.Fatalf("forward=%t: %v", forward, got.Header)
		}
		got.Body.Close()
	}
}

func TestRawTLSHandsOffPendingSlot(t *testing.T) {
	cert, key, _, err := control.SelfSignedTunnelCertificate("listener.example.test")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(cert))
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{}
	e.maxConnections.Store(1)
	e.h2Certificate.Store(&pair)
	listener := newCamouflageTLSListener(context.Background(), raw, &tls.Config{Certificates: []tls.Certificate{pair}}, e, true)
	defer listener.Close()
	for i := 0; i < 2; i++ {
		accepted := make(chan net.Conn, 1)
		go func() { c, _ := listener.Accept(); accepted <- c }()
		d := net.Dialer{Timeout: 3 * time.Second}
		c, err := tls.DialWithDialer(&d, "tcp", raw.Addr().String(), &tls.Config{ServerName: "listener.example.test", RootCAs: roots})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case peer := <-accepted:
			if peer == nil {
				t.Fatal("no accepted connection")
			}
			defer peer.Close()
		case <-time.After(3 * time.Second):
			t.Fatal("handoff timed out")
		}
		until := time.Now().Add(3 * time.Second)
		for {
			listener.mu.Lock()
			pending := listener.pending
			listener.mu.Unlock()
			if pending == 0 {
				break
			}
			if time.Now().After(until) {
				t.Fatal("slot still occupied by established connection")
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func TestFallbackCookiesInEarlyHintsAndTrailers(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "hint=fixture")
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Del("Set-Cookie")
		w.Header().Add("Trailer", "Set-Cookie")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
		w.Header().Set("Set-Cookie", "trailer=fixture")
	}))
	defer up.Close()
	e := &Engine{}
	e.node.Store(&pb.NodeConfig{Tls: &pb.TLSServerConfig{FallbackUrl: up.URL}})
	front := httptest.NewServer(http.HandlerFunc(e.camouflage))
	defer front.Close()
	hints := make(chan http.Header, 1)
	req, _ := http.NewRequest(http.MethodGet, front.URL, nil)
	trace := &httptrace.ClientTrace{Got1xxResponse: func(_ int, h textproto.MIMEHeader) error { hints <- http.Header(h).Clone(); return nil }}
	res, err := front.Client().Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || string(body) != "ok" || res.Trailer.Get("Set-Cookie") != "" {
		t.Fatalf("unexpected final response %q %v %v", body, res.Trailer, err)
	}
	select {
	case hint := <-hints:
		if hint.Get("Set-Cookie") != "" {
			t.Fatal("informational response forwarded cookie")
		}
	default:
		t.Fatal("expected informational response")
	}
}

func TestPendingTLSLimitTracksNodeSettings(t *testing.T) {
	e := &Engine{}
	l := &camouflageTLSListener{engine: e, limitPending: true}
	e.maxConnections.Store(1)
	if !l.reservePending() || l.reservePending() {
		t.Fatal("small handshake limit ignored")
	}
	e.maxConnections.Store(2)
	if !l.reservePending() || l.reservePending() {
		t.Fatal("updated handshake limit ignored")
	}
	l.pending--
	if !l.reservePending() {
		t.Fatal("released handshake slot unavailable")
	}
}
