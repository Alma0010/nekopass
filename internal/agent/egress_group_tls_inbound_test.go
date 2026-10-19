package agent

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/protobuf/proto"
)

func TestTLSInboundSNIThroughEgressGroupH2(t *testing.T) {
	target, root := inboundEcho(t, true)
	engine, config, address, clientConfig := inboundFixture(t, root, target)
	var peers []*pb.EgressPeer
	var rule *pb.Rule
	for i := 0; i < 2; i++ {
		exit, _, exitConfig, exitRule := h2Fixture(t, target)
		exitConfig.Node.TunnelProtocol = "tls_h2"
		exitConfig.EgressRules[0].Protocol = "tcp"
		exitConfig.Revision++
		if err := exit.Apply(exitConfig); err != nil {
			t.Fatal(err)
		}
		exitRule.TunnelProtocol = "tls_h2"
		rule = exitRule
		peers = append(peers, &pb.EgressPeer{NodeId: int64(30 + i), Host: exitRule.TunnelHost, Port: exitRule.TunnelPort, Token: exitRule.TunnelToken, Protocol: "tls_h2", Tls: exitRule.Tls, Weight: 1})
	}

	rule = proto.Clone(rule).(*pb.Rule)
	rule.Enabled = true
	rule.EgressNodeId = 0
	rule.EgressGroupId = 77
	rule.EgressPeers = peers
	rule.EgressStrategy = "round_robin"
	rule.HealthIntervalSeconds = 60
	rule.HealthFailures = 2
	rule.HealthSuccesses = 2
	rule.Protocol = "tcp"
	rule.TlsInboundId = 1
	rule.SniNames = []string{"a.example.test"}
	rule.Targets = []string{target}
	config.Users = []*pb.UserPolicy{{Id: rule.UserId, QuotaEpoch: rule.QuotaEpoch, Enabled: true, Issued: 1 << 20, SpeedBps: 1 << 30, MaxConnections: 100}}
	config.Rules = []*pb.Rule{rule}
	config.Revision++
	if err := engine.Apply(config); err != nil {
		t.Fatal(err)
	}
	clientConfig.NextProtos = []string{"http/1.1", "h2"}
	clientConfig.MinVersion = tls.VersionTLS12
	conn, err := tls.Dial("tcp", address, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	const payload = "sni-via-egress-group"
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != payload {
		t.Fatalf("SNI TLS data did not traverse the egress group: got %q, err %v", got, err)
	}
}

func TestGroupSlowTargetResponseRespectsConfiguredBudget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var header [86]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(header[84:86]))
		if n < 1 || n > 512 {
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(n)); err != nil {
			return
		}
		// Model a slow business target connection at the exit. The exit
		// eventually authenticates the tunnel and accepts the request.
		time.Sleep(2500 * time.Millisecond)
		_, _ = conn.Write([]byte{0})
	}()

	var queued atomic.Int64
	m := newEgressManager(nil, &queued)
	defer m.close()
	r := &pb.Rule{Id: 1, UserId: 1, IngressNodeId: 2, EgressGroupId: 3, Enabled: true, Protocol: "tcp", EgressStrategy: "round_robin", HealthIntervalSeconds: 60, HealthFailures: 1, HealthSuccesses: 2,
		EgressPeers: []*pb.EgressPeer{{NodeId: 4, Host: "127.0.0.1", Port: int32(listener.Addr().(*net.TCPAddr).Port), Token: strings.Repeat("a", 64), Protocol: "plain_tcp", Weight: 1}}}
	m.update([]*pb.Rule{r})
	key := peerKey(peerRule(r, r.EgressPeers[0]))
	m.mu.Lock()
	m.peers[key].next = time.Now().Add(time.Minute)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, release, err := m.dial(ctx, ctx, r, "target.test:443", false)
	if release != nil {
		release()
	}
	if err != nil {
		t.Fatalf("slow successful target response exceeded the configured eight-second dial budget: %v", err)
	}
	if conn != nil {
		conn.Close()
	}
	m.mu.Lock()
	blocked := m.schedules[scheduleKey(r)].health[key].blocked
	m.mu.Unlock()
	if blocked {
		t.Fatal("slow but successful target response blocked the healthy exit")
	}
}

func TestGroupLiveExitFailureAndRecoveryUsesStandbyAndReturnsToPrimary(t *testing.T) {
	target, _ := inboundEcho(t, false)
	primary, _, primaryConfig, primaryRule := h2Fixture(t, target)
	standby, _, standbyConfig, standbyRule := h2Fixture(t, target)
	for _, fixture := range []struct {
		engine *Engine
		config *pb.ControlMessage
	}{{primary, primaryConfig}, {standby, standbyConfig}} {
		fixture.config.Node.TunnelProtocol = "tls_h2"
		fixture.config.EgressRules[0].Protocol = "tcp"
		fixture.config.Revision++
		if err := fixture.engine.Apply(fixture.config); err != nil {
			t.Fatal(err)
		}
	}

	h2 := newH2Transport()
	defer h2.Close()
	var queued atomic.Int64
	m := newEgressManager(h2, &queued)
	defer m.close()
	r := proto.Clone(primaryRule).(*pb.Rule)
	r.Enabled = true
	r.EgressNodeId = 0
	r.EgressGroupId = 90
	r.Protocol = "tcp"
	r.EgressStrategy = "round_robin"
	r.HealthIntervalSeconds = 2
	r.HealthFailures = 1
	r.HealthSuccesses = 2
	r.EgressPeers = []*pb.EgressPeer{
		{NodeId: 30, Host: primaryRule.TunnelHost, Port: primaryRule.TunnelPort, Token: primaryRule.TunnelToken, Protocol: "tls_h2", Tls: primaryRule.Tls, Weight: 1},
		{NodeId: 31, Host: standbyRule.TunnelHost, Port: standbyRule.TunnelPort, Token: standbyRule.TunnelToken, Protocol: "tls_h2", Tls: standbyRule.Tls, Weight: 1, Standby: true},
	}
	m.update([]*pb.Rule{r})
	primaryKey := peerKey(peerRule(r, r.EgressPeers[0]))
	standbyKey := peerKey(peerRule(r, r.EgressPeers[1]))
	m.mu.Lock()
	m.peers[primaryKey].next = time.Now().Add(time.Minute)
	m.peers[standbyKey].next = time.Now().Add(time.Minute)
	m.mu.Unlock()

	dialAndCheck := func(want string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		conn, release, err := m.dial(ctx, ctx, r, target, false)
		if err != nil {
			t.Fatal("group dial failed:", err)
		}
		defer conn.Close()
		defer release()
		m.mu.Lock()
		primaryActive, standbyActive := m.peers[primaryKey].active, m.peers[standbyKey].active
		m.mu.Unlock()
		if want == primaryKey && (primaryActive != 1 || standbyActive != 0) || want == standbyKey && (standbyActive != 1 || primaryActive != 0) {
			t.Fatalf("new connection used wrong exit: primary active=%d, standby active=%d, want %s", primaryActive, standbyActive, want)
		}
	}

	// With both exits available, the primary carries a new connection.
	dialAndCheck(primaryKey)

	// Stop only the primary listener, retaining the same port for its later restart.
	stopped := proto.Clone(primaryConfig).(*pb.ControlMessage)
	stopped.Revision++
	stopped.Node.TunnelExitEnabled = false
	if err := primary.Apply(stopped); err != nil {
		t.Fatal("stop primary exit:", err)
	}
	dialAndCheck(standbyKey)

	// A reused H2 connection can fail with EOF without proving that socket
	// establishment is broken. Let the dedicated probe establish the health
	// failure and move the peer into standby state.
	m.mu.Lock()
	m.peers[primaryKey].next = time.Now().Add(100 * time.Millisecond)
	m.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		blocked := m.schedules[scheduleKey(r)].health[primaryKey].blocked
		m.mu.Unlock()
		if blocked {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	m.mu.Lock()
	blocked := m.schedules[scheduleKey(r)].health[primaryKey].blocked
	m.mu.Unlock()
	if !blocked {
		t.Fatal("failed primary was not blocked by its dedicated health probe")
	}

	// Restart the same listener and wait for two independent health probes.
	restarted := proto.Clone(primaryConfig).(*pb.ControlMessage)
	restarted.Revision += 2
	if err := primary.Apply(restarted); err != nil {
		t.Fatal("restart primary exit on the same port:", err)
	}
	m.mu.Lock()
	m.peers[primaryKey].next = time.Now().Add(100 * time.Millisecond)
	m.mu.Unlock()
	deadline = time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		state := m.schedules[scheduleKey(r)].health[primaryKey]
		recovered := !state.blocked && state.successes >= 2
		m.mu.Unlock()
		if recovered {
			dialAndCheck(primaryKey)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("primary did not recover after two authenticated health probes")
}
