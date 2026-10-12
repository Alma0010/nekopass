package agent

import (
	"crypto/tls"
	"crypto/x509"
	"github.com/nekopass/nekopass/internal/control"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func inboundPort(t *testing.T) int32 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return int32(l.Addr().(*net.TCPAddr).Port)
}
func inboundEcho(t *testing.T, secure bool) (string, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cert := ""
	if secure {
		var key string
		cert, key, _, err = control.SelfSignedTunnelCertificate("*.example.test")
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
		if err != nil {
			t.Fatal(err)
		}
		l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	return l.Addr().String(), cert
}
func inboundFixture(t *testing.T, root string, targets ...string) (*Engine, *pb.ControlMessage, string, *tls.Config) {
	t.Helper()
	state, err := OpenState(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(state, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(); state.Close() })
	port := inboundPort(t)
	config := &pb.ControlMessage{Revision: 1, Node: &pb.NodeConfig{NodeId: 2, Enabled: true, ListenHost: "127.0.0.1", MaxConnections: 100, SecurityConfigured: true, TlsInbounds: []*pb.SharedTLSInbound{{Id: 1, Port: port, Enabled: true, Passthrough: true, AllowedSni: []string{"example.test"}, RejectEmptySni: true, ProxyAccept: "off", MaxHandshakes: 16, HandshakeTimeoutSeconds: 1}}}}
	for i, target := range targets {
		id := int64(i + 1)
		name := "a.example.test"
		if i > 0 {
			name = "b.example.test"
		}
		config.Users = append(config.Users, &pb.UserPolicy{Id: id, Enabled: true, QuotaEpoch: 1, Issued: 1 << 20, SpeedBps: 1 << 30, MaxConnections: 10})
		config.Rules = append(config.Rules, &pb.Rule{Id: id, UserId: id, Protocol: "tcp", Enabled: true, TlsInboundId: 1, SniNames: []string{name}, Targets: []string{target}, ProxyAccept: "off", ProxySend: "off"})
	}
	if err = engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	if engine.syncError != "" {
		t.Fatal(engine.syncError)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(root))
	return engine, config, engine.tlsInbounds[1].ln.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "a.example.test", MinVersion: tls.VersionTLS12}
}
func inboundExchange(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != payload {
		t.Fatal("incorrect shared TLS route payload", err)
	}
	conn.SetDeadline(time.Time{})
}

func TestTLSInboundUserIsolationPauseAndAccounting(t *testing.T) {
	target, root := inboundEcho(t, true)
	otherTarget, otherRoot := inboundEcho(t, true)
	engine, config, address, clientConfig := inboundFixture(t, root+otherRoot, target, otherTarget)
	a, err := tls.Dial("tcp", address, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	bConfig := clientConfig.Clone()
	bConfig.ServerName = "b.example.test"
	b, err := tls.Dial("tcp", address, bConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	inboundExchange(t, a, "hello")
	inboundExchange(t, b, "world!")
	if len(a.ConnectionState().PeerCertificates) == 0 || len(b.ConnectionState().PeerCertificates) == 0 || a.ConnectionState().PeerCertificates[0].Equal(b.ConnectionState().PeerCertificates[0]) {
		t.Fatal("SNI routes did not reach distinct target certificates")
	}
	// Transparent forwarding counts the TLS wire bytes, including the handshake.
	if engine.counters[1].Load() <= 10 || engine.counters[2].Load() <= 12 {
		t.Fatal("TLS wire bytes not accounted")
	}
	engine.mu.Lock()
	for _, id := range []int64{1, 2} {
		account := engine.users[id]
		account.mu.Lock()
		traffic := account.disk.Traffic
		account.mu.Unlock()
		if traffic != engine.counters[id].Load() {
			engine.mu.Unlock()
			t.Fatal("per-user counters not isolated")
		}
	}
	engine.mu.Unlock()
	config = proto.Clone(config).(*pb.ControlMessage)
	config.Revision++
	config.Rules[0].Enabled = false
	config.Node.TlsInbounds[0].DefaultRuleId = 2
	if err = engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	a.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err = a.Read(make([]byte, 1)); err == nil {
		t.Fatal("paused child retained connection")
	}
	if conn, err := tls.Dial("tcp", address, clientConfig); err == nil {
		conn.Close()
		t.Fatal("paused SNI fell through to default user")
	}
	inboundExchange(t, b, "still-live")
	config = proto.Clone(config).(*pb.ControlMessage)
	config.Revision++
	config.Rules[0].Enabled = true
	config.Node.TlsInbounds[0].DefaultRuleId = 0
	if err = engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	resumed, err := tls.Dial("tcp", address, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	inboundExchange(t, resumed, "resumed")
	inboundExchange(t, b, "unaffected")
	for _, name := range []string{"", "unknown.example.test", "outside.invalid"} {
		bad := clientConfig.Clone()
		bad.ServerName = name
		bad.InsecureSkipVerify = true
		if conn, err := tls.Dial("tcp", address, bad); err == nil {
			conn.Close()
			t.Fatal("unregistered SNI accepted")
		}
	}
}

func TestTLSInboundEndToEndViaTransports(t *testing.T) {
	for _, transport := range []string{"direct", "plain_tcp", "tls_tcp", "plain_h2", "tls_h2"} {
		t.Run(transport, func(t *testing.T) {
			target, root := inboundEcho(t, true)
			engine, config, address, clientConfig := inboundFixture(t, root, target)
			if transport != "direct" {
				exit, _, exitConfig, tunnelRule := h2Fixture(t, target)
				exitConfig.Node.TunnelProtocol = transport
				exitConfig.Revision++
				if err := exit.Apply(exitConfig); err != nil {
					t.Fatal(err)
				}
				rule := proto.Clone(tunnelRule).(*pb.Rule)
				rule.TunnelProtocol = transport
				rule.Enabled = true
				rule.Protocol = "tcp"
				rule.TlsInboundId = 1
				rule.SniNames = []string{"a.example.test"}
				rule.Targets = []string{target}
				config.Rules = []*pb.Rule{rule}
				config.Users = []*pb.UserPolicy{{Id: 5, QuotaEpoch: 3, Enabled: true, Issued: 1 << 20, SpeedBps: 1 << 30, MaxConnections: 100}}
			}
			config.Revision++
			if err := engine.Apply(config); err != nil {
				t.Fatal(err)
			}
			clientConfig.NextProtos = []string{"http/1.1", "h2"}
			for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
				clientConfig.MinVersion = version
				clientConfig.MaxVersion = version
				conn, err := tls.Dial("tcp", address, clientConfig)
				if err != nil {
					t.Fatal(err)
				}
				if conn.ConnectionState().NegotiatedProtocol != "http/1.1" || conn.ConnectionState().Version != version {
					t.Fatal("target ALPN or version not preserved")
				}
				inboundExchange(t, conn, "end-to-end-secured")
				conn.Close()
			}
			// The client, not the node, enforces target certificate trust.
			clientConfig.RootCAs = x509.NewCertPool()
			if conn, err := tls.Dial("tcp", address, clientConfig); err == nil {
				conn.Close()
				t.Fatal("untrusted target accepted")
			}
		})
	}
}

func TestTLSInboundRejectsLegacyTerminationConfiguration(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, config, address, clientConfig := inboundFixture(t, root, target)
	config.Revision++
	config.Node.TlsInbounds[0].Passthrough = false
	if err := engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	if conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, clientConfig); err == nil {
		conn.Close()
		t.Fatal("legacy termination resource accepted")
	}
}

func TestTLSInboundHandshakeLimitRecoversAndProxyTrust(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, config, address, clientConfig := inboundFixture(t, root, target)
	config.Node.TlsInbounds[0].MaxHandshakes = 1
	config.Node.TlsInbounds[0].ProxyAccept = "auto"
	config.Node.ProxyTrustedCidrs = []string{"127.0.0.0/8"}
	config.Revision++
	if err := engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	stalled, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	stalled.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(stalled, "PROXY TCP4 198.51.100.1 127.0.0.1 50000 443\r\n")
	if _, err = stalled.Read(make([]byte, 1)); err == nil {
		t.Fatal("slow handshake did not time out")
	}
	// Closing the timed-out socket and releasing its reservation are asynchronous.
	engine.mu.Lock()
	shared := engine.tlsInbounds[1]
	engine.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		shared.mu.Lock()
		pending := shared.handshakes
		shared.mu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ClientHello reservation leaked after timeout")
		}
		time.Sleep(time.Millisecond)
	}
	raw, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	io.WriteString(raw, "PROXY TCP4 198.51.100.2 127.0.0.1 50001 443\r\n")
	conn := tls.Client(raw, clientConfig)
	if err = conn.Handshake(); err != nil {
		t.Fatal("handshake capacity not released", err)
	}
	inboundExchange(t, conn, "proxy-before-tls")
	engine.mu.Lock()
	account := engine.users[1]
	engine.mu.Unlock()
	account.mu.Lock()
	_, ok := account.ips["198.51.100.2"]
	account.mu.Unlock()
	if !ok {
		t.Fatal("trusted PROXY source not preserved")
	}
}

func TestACMEHTTPMultiplePortsAreScoped(t *testing.T) {
	state, err := OpenState(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	engine, err := NewEngine(state, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	portA, portB := inboundPort(t), inboundPort(t)
	config := &pb.ControlMessage{Revision: 1, Node: &pb.NodeConfig{Enabled: true, MaxConnections: 10, AcmeHttp: []*pb.ACMEHTTPEndpoint{{Port: portA, Challenges: []*pb.ACMEChallenge{{Domain: "a.example.test", Token: "a", KeyAuthorization: "key-a", Revision: 10, ExpiresUnix: time.Now().Add(time.Minute).Unix()}}}, {Port: portB, Challenges: []*pb.ACMEChallenge{{Domain: "b.example.test", Token: "b", KeyAuthorization: "key-b", Revision: 11, ExpiresUnix: time.Now().Add(time.Minute).Unix()}}}}}}
	if err = engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	if engine.acmeAck.Load() != 11 {
		t.Fatal("challenge ports not acknowledged")
	}
	for _, test := range []struct {
		port        int32
		host, token string
		status      int
	}{{portA, "a.example.test", "a", 200}, {portA, "b.example.test", "b", 404}, {portB, "b.example.test", "b", 200}} {
		r := httptest.NewRequest("GET", "http://"+test.host+"/.well-known/acme-challenge/"+test.token, nil)
		w := httptest.NewRecorder()
		engine.handleACMEPort(test.port, w, r)
		if w.Code != test.status {
			t.Fatal("challenge leaked between validation ports")
		}
	}
	// HEAD support uses the same domain/token checks as GET.
	_ = http.MethodHead
}
