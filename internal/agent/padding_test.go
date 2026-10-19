package agent

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/protobuf/proto"
)

func TestRawTLSPaddingDoesNotChargeFramingOrPadding(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	exit, _, cfg, rule := h2Fixture(t, target.Addr().String())
	cfg.Node.TunnelProtocol = "tls_tcp"
	cfg.Node.Tls.PaddingEnabled = true
	rule.TunnelProtocol = "tls_tcp"
	rule.Tls.PaddingEnabled = true
	if err = exit.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	entry, err := NewEngine(state, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	rule.Enabled = true
	rule.Protocol = "tcp"
	rule.ListenHost = "127.0.0.1"
	rule.Targets = []string{target.Addr().String()}
	if err = entry.Apply(&pb.ControlMessage{Revision: 1, Node: &pb.NodeConfig{Enabled: true, ListenHost: "127.0.0.1", MaxConnections: 100, SecurityConfigured: true, IdleTimeoutSeconds: 300, DialTimeoutSeconds: 8}, Users: []*pb.UserPolicy{{Id: 5, Enabled: true, QuotaEpoch: 3, Issued: 1 << 20}}, Rules: []*pb.Rule{rule}}); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", entry.listeners[rule.Id].ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	payload := bytes.Repeat([]byte{42}, 12345)
	if _, err = c.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(c, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatal(err)
	}
	if err = c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if extra, err := io.ReadAll(c); err != nil || len(extra) != 0 {
		t.Fatal("padded FIN changed business data or half-close", err, len(extra))
	}
	want := int64(2 * len(payload))
	deadline := time.Now().Add(time.Second)
	for entry.counters[rule.Id].Load() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if entry.counters[rule.Id].Load() != want {
		t.Fatal("rule charged padding")
	}
	entry.users[5].mu.Lock()
	traffic := entry.users[5].disk.Traffic
	entry.users[5].mu.Unlock()
	if traffic != want {
		t.Fatal("user charged padding", traffic, want)
	}
}

func TestRawTLSPaddingForwardingHalfCloseAndAuthentication(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	exit, _, cfg, rule := h2Fixture(t, target.Addr().String())
	cfg.Node.TunnelProtocol = "tls_tcp"
	rule.TunnelProtocol = "tls_tcp"
	cfg.Node.Tls.PaddingEnabled = true
	rule.Tls.PaddingEnabled = true
	cfg.Node.Tls.PaddingScheme = []string{"stop=8", "0=30-30", "1=100-200", "2=400-500,c,500-1000", "3=500-1000"}
	rule.Tls.PaddingScheme = cfg.Node.Tls.PaddingScheme
	if err = exit.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, fingerprint := range []string{"off", "chrome", "firefox"} {
		rule.Tls.Fingerprint = fingerprint
		c, err := dialTunnel(ctx, rule, target.Addr().String())
		if err != nil {
			t.Fatal(fingerprint, err)
		}
		payload := bytes.Repeat([]byte{0, 255, 1, 2, 3}, 100000)
		written := make(chan error, 1)
		go func() { _, err := c.Write(payload); written <- err }()
		got := make([]byte, len(payload))
		if _, err = io.ReadFull(c, got); err != nil || !bytes.Equal(got, payload) {
			c.Close()
			t.Fatal("padding changed target data", err)
		}
		if err = <-written; err != nil {
			t.Fatal(err)
		}
		if err = c.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		if n, err := c.Read(make([]byte, 1)); n != 0 || err != io.EOF {
			t.Fatal("padding broke half close", n, err)
		}
		c.Close()
	}
	bad := proto.Clone(rule).(*pb.Rule)
	bad.TunnelToken = strings.Repeat("b", 64)
	if c, err := dialTunnel(ctx, bad, target.Addr().String()); err == nil {
		c.Close()
		t.Fatal("padding bypassed authentication")
	}
	bad = proto.Clone(rule).(*pb.Rule)
	bad.Tls.PaddingEnabled = false
	if c, err := dialTunnel(ctx, bad, target.Addr().String()); err == nil {
		c.Close()
		t.Fatal("required padding silently downgraded")
	}
	cfg.Node.Tls.PaddingEnabled = false
	if err = exit.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if c, err := dialTunnel(ctx, rule, target.Addr().String()); err == nil {
		c.Close()
		t.Fatal("padding enabled against non-padding exit")
	}
}

func TestRawTLSPaddingUDPAndOtherModes(t *testing.T) {
	target, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := target.ReadFrom(buf)
			if err != nil {
				return
			}
			target.WriteTo(buf[:n], addr)
		}
	}()
	exit, _, cfg, rule := h2Fixture(t, target.LocalAddr().String())
	cfg.Node.TunnelProtocol = "tls_tcp"
	rule.TunnelProtocol = "tls_tcp"
	cfg.Node.Tls.PaddingEnabled = true
	rule.Tls.PaddingEnabled = true
	cfg.EgressRules[0].Protocol = "udp"
	rule.Protocol = "udp"
	if err = exit.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c, err := dialRawDatagramTunnel(ctx, rule, target.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	for _, n := range []int{0, 1, 123, 8192, 65000} {
		payload := bytes.Repeat([]byte{42}, n)
		if sent, err := c.Write(payload); sent != n || err != nil {
			t.Fatal(sent, err)
		}
		buf := make([]byte, 65535)
		got, err := c.Read(buf)
		if got != n || err != nil || !bytes.Equal(buf[:got], payload) {
			t.Fatal("UDP boundary changed", n, got, err)
		}
	}
	for _, mode := range []string{"plain_tcp", "plain_h2", "tls_h2", "plain_udp", "dtls_udp"} {
		r := &pb.Rule{TunnelProtocol: mode, Tls: &pb.TLSClientConfig{PaddingEnabled: true, PaddingScheme: []string{"invalid"}}}
		if p, err := rulePadding(r); p != nil || err != nil {
			t.Fatal("padding activated outside raw TCP/TLS", mode)
		}
	}
}
