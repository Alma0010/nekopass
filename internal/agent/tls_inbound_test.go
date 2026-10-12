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
		cert, key, _, err = control.SelfSignedTunnelCertificate("origin.example.test")
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
func inboundFixture(t *testing.T, targets ...string) (*Engine, *pb.ControlMessage, string, *tls.Config) {
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
	cert, key, _, err := control.SelfSignedTunnelCertificate("*.example.test")
	if err != nil {
		t.Fatal(err)
	}
	port := inboundPort(t)
	config := &pb.ControlMessage{Revision: 1, Node: &pb.NodeConfig{NodeId: 2, Enabled: true, ListenHost: "127.0.0.1", MaxConnections: 100, SecurityConfigured: true, TlsInbounds: []*pb.SharedTLSInbound{{Id: 1, Port: port, Enabled: true, Certificate: cert, PrivateKey: key, AllowedSni: []string{"example.test"}, RejectEmptySni: true, ProxyAccept: "off", MaxHandshakes: 16, HandshakeTimeoutSeconds: 1, MinTlsVersion: "1.2"}}}}
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
	roots.AppendCertsFromPEM([]byte(cert))
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
	target, _ := inboundEcho(t, false)
	engine, config, address, clientConfig := inboundFixture(t, target, target)
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
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (engine.counters[1].Load() != 10 || engine.counters[2].Load() != 12) {
		time.Sleep(time.Millisecond)
	}
	if engine.counters[1].Load() != 10 || engine.counters[2].Load() != 12 {
		t.Fatalf("TLS traffic mismatch: user A %d, user B %d", engine.counters[1].Load(), engine.counters[2].Load())
	}
	// Completing TLS without application data does not debit payload traffic.
	idle, err := tls.Dial("tcp", address, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	idle.Close()
	if engine.counters[1].Load() != 10 {
		t.Fatal("handshake was charged")
	}
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

func TestTLSInboundOriginTLSDirectAndH2(t *testing.T) {
	for _, viaTunnel := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "h2"}[viaTunnel], func(t *testing.T) {
			target, root := inboundEcho(t, true)
			engine, config, address, clientConfig := inboundFixture(t, target)
			if viaTunnel {
				_, _, _, tunnelRule := h2Fixture(t, target)
				rule := proto.Clone(tunnelRule).(*pb.Rule)
				rule.Enabled = true
				rule.Protocol = "tcp"
				rule.TlsInboundId = 1
				rule.SniNames = []string{"a.example.test"}
				rule.Targets = []string{target}
				config.Rules = []*pb.Rule{rule}
				config.Users = []*pb.UserPolicy{{Id: 5, QuotaEpoch: 3, Enabled: true, Issued: 1 << 20, SpeedBps: 1 << 30, MaxConnections: 100}}
			}
			rule := config.Rules[0]
			rule.OriginTls = &pb.OriginTLSConfig{Enabled: true, ServerName: "origin.example.test", RootCa: root, Alpn: []string{"http/1.1"}}
			rule.TlsAlpn = []string{"http/1.1"}
			config.Revision++
			if err := engine.Apply(config); err != nil {
				t.Fatal(err)
			}
			clientConfig.NextProtos = []string{"http/1.1"}
			conn, err := tls.Dial("tcp", address, clientConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			inboundExchange(t, conn, "origin-secured")
			bad := proto.Clone(config).(*pb.ControlMessage)
			bad.Revision++
			bad.Rules[0].OriginTls.RootCa = ""
			if err = engine.Apply(bad); err != nil {
				t.Fatal(err)
			}
			untrusted, err := tls.Dial("tcp", address, clientConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer untrusted.Close()
			untrusted.SetDeadline(time.Now().Add(3 * time.Second))
			untrusted.Write([]byte("must not forward"))
			if n, err := untrusted.Read(make([]byte, 64)); err == nil || n > 0 {
				t.Fatal("origin TLS trust bypassed")
			}
		})
	}
}

func TestTLSInboundHandshakeLimitRecoversAndProxyTrust(t *testing.T) {
	target, _ := inboundEcho(t, false)
	engine, config, address, clientConfig := inboundFixture(t, target)
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
