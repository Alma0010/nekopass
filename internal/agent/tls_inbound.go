package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"github.com/nekopass/nekopass/internal/tlsinbound"
	"google.golang.org/protobuf/proto"
)

type inboundRoute struct {
	l       *listener
	account *Account
	count   *atomic.Int64
}
type inboundSnapshot struct {
	config  *pb.SharedTLSInbound
	cert    *tls.Certificate
	names   map[string]*inboundRoute
	routes  map[int64]*inboundRoute
	claimed map[string]bool
}
type tlsInboundListener struct {
	ln         net.Listener
	address    string
	ctx        context.Context
	cancel     context.CancelFunc
	snapshot   atomic.Pointer[inboundSnapshot]
	mu         sync.Mutex
	handshakes int
	peers      map[string]int
}

func (l *tlsInboundListener) reserve(peer string, cap int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handshakes >= cap || l.peers[peer] >= min(16, cap) {
		return false
	}
	l.handshakes++
	l.peers[peer]++
	return true
}
func (l *tlsInboundListener) release(peer string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handshakes--
	l.peers[peer]--
	if l.peers[peer] == 0 {
		delete(l.peers, peer)
	}
}

// Called with Engine.mu held. Routes are immutable and independently cancellable.
func (e *Engine) configureTLSInbounds(c *pb.ControlMessage, restart bool) []string {
	if e.tlsInbounds == nil {
		e.tlsInbounds = map[int64]*tlsInboundListener{}
	}
	wanted := map[int64]*pb.SharedTLSInbound{}
	if c.Node != nil && c.Node.Enabled {
		for _, cfg := range c.Node.TlsInbounds {
			if cfg.Enabled {
				wanted[cfg.Id] = cfg
			}
		}
	}
	for id, l := range e.tlsInbounds {
		cfg := wanted[id]
		old := l.snapshot.Load()
		if cfg == nil || old.config.Port != cfg.Port || l.address != net.JoinHostPort(c.Node.ListenHost, strconv.Itoa(int(cfg.Port))) {
			l.cancel()
			l.ln.Close()
			for _, r := range old.routes {
				r.l.cancel()
			}
			delete(e.tlsInbounds, id)
		}
	}
	var errs []string
	for id, cfg := range wanted {
		pair, err := tlsinbound.Certificate(cfg.Certificate, cfg.PrivateKey)
		if err != nil {
			if l := e.tlsInbounds[id]; l != nil {
				l.cancel()
				l.ln.Close()
				for _, r := range l.snapshot.Load().routes {
					r.l.cancel()
				}
				delete(e.tlsInbounds, id)
			}
			errs = append(errs, fmt.Sprintf("TLS inbound %d: certificate unavailable", id))
			continue
		}
		l := e.tlsInbounds[id]
		var old *inboundSnapshot
		if l != nil {
			old = l.snapshot.Load()
		}
		snapshot := &inboundSnapshot{config: cfg, cert: pair, names: map[string]*inboundRoute{}, routes: map[int64]*inboundRoute{}, claimed: map[string]bool{}}
		for _, rule := range c.Rules {
			if rule.TlsInboundId == id {
				for _, name := range rule.SniNames {
					snapshot.claimed[name] = true
				}
			}
			if rule.TlsInboundId != id || !rule.Enabled || rule.Protocol != "tcp" || e.users[rule.UserId] == nil || !e.users[rule.UserId].valid() {
				continue
			}
			names, err := tlsinbound.Names(rule.SniNames)
			if err != nil {
				errs = append(errs, fmt.Sprintf("TLS rule %d: invalid SNI", rule.Id))
				continue
			}
			allow := tlsinbound.Config{AllowedSNI: cfg.AllowedSni}
			valid := true
			for _, name := range names {
				if !allow.Allows(name) || pair.Leaf.VerifyHostname(name) != nil || snapshot.names[name] != nil {
					valid = false
				}
			}
			if !valid {
				errs = append(errs, fmt.Sprintf("TLS rule %d: SNI unavailable", rule.Id))
				continue
			}
			var route *inboundRoute
			if old != nil && !restart {
				route = old.routes[rule.Id]
				if route != nil && (route.account != e.users[rule.UserId] || !proto.Equal(route.l.rule, rule)) {
					route = nil
				}
			}
			if route == nil {
				ctx, cancel := context.WithCancel(context.Background())
				runtime := newRuleRuntime(rule)
				runtime.configureNode(e.nodeConfiguration())
				runtime.h2 = e.h2
				runtime.dialTarget = e.dialTarget
				count := e.counters[rule.Id]
				if count == nil {
					count = &atomic.Int64{}
					e.counters[rule.Id] = count
				}
				route = &inboundRoute{l: &listener{rule: rule, runtime: runtime, ctx: ctx, cancel: cancel}, account: e.users[rule.UserId], count: count}
			}
			snapshot.routes[rule.Id] = route
			for _, name := range names {
				snapshot.names[name] = route
			}
		}
		if old != nil {
			for id, r := range old.routes {
				if snapshot.routes[id] != r {
					r.l.cancel()
				}
			}
		}
		if l == nil {
			ln, err := net.Listen("tcp", net.JoinHostPort(c.Node.ListenHost, strconv.Itoa(int(cfg.Port))))
			if err != nil {
				for _, r := range snapshot.routes {
					r.l.cancel()
				}
				errs = append(errs, fmt.Sprintf("TLS inbound %d: %v", id, err))
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			l = &tlsInboundListener{ln: ln, address: net.JoinHostPort(c.Node.ListenHost, strconv.Itoa(int(cfg.Port))), ctx: ctx, cancel: cancel, peers: map[string]int{}}
			e.tlsInbounds[id] = l
			l.snapshot.Store(snapshot)
			e.wg.Add(1)
			go e.acceptTLSInbound(l)
		} else {
			l.snapshot.Store(snapshot)
		}
	}
	return errs
}

func (e *Engine) acceptTLSInbound(l *tlsInboundListener) {
	defer e.wg.Done()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return
		}
		cfg := l.snapshot.Load().config
		peer, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		active := e.connections.Add(1)
		if cap := e.maxConnections.Load(); cap > 0 && active > cap {
			e.connections.Add(-1)
			conn.Close()
			continue
		}
		if !l.reserve(peer, int(cfg.MaxHandshakes)) {
			e.connections.Add(-1)
			conn.Close()
			continue
		}
		e.wg.Add(1)
		go func() { defer e.wg.Done(); defer e.connections.Add(-1); e.serveTLSInbound(l, conn, peer) }()
	}
}
func (e *Engine) serveTLSInbound(l *tlsInboundListener, raw net.Conn, peer string) {
	defer raw.Close()
	stopListener := context.AfterFunc(l.ctx, func() { raw.Close() })
	defer stopListener()
	released := false
	releaseHandshake := func() {
		if !released {
			l.release(peer)
			released = true
		}
	}
	defer releaseHandshake()
	snapshot := l.snapshot.Load()
	cfg := snapshot.config
	timeout := time.Duration(cfg.HandshakeTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(l.ctx, timeout)
	stopHandshake := context.AfterFunc(handshakeCtx, func() { raw.Close() })
	defer cancelHandshake()
	defer stopHandshake()
	_ = raw.SetDeadline(time.Now().Add(timeout))
	node := e.nodeConfiguration()
	client, addresses, err := readProxy(raw, cfg.ProxyAccept, node.ProxyTrustedCidrs)
	if err != nil {
		return
	}
	var selected *inboundRoute
	var stopRoute func() bool
	defer func() {
		if selected != nil {
			selected.l.runtime.close()
			selected.account.active.Add(-1)
		}
		if stopRoute != nil {
			stopRoute()
		}
	}()
	config := &tls.Config{MinVersion: tls.VersionTLS12, SessionTicketsDisabled: true}
	if cfg.MinTlsVersion == "1.3" {
		config.MinVersion = tls.VersionTLS13
	}
	config.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if l.ctx.Err() != nil {
			return nil, errors.New("listener closed")
		}
		if selected == nil {
			name := hello.ServerName
			if name == "" && cfg.RejectEmptySni {
				return nil, errors.New("missing SNI")
			}
			if name != "" {
				var err error
				name, err = tlsinbound.Name(name)
				if err != nil {
					return nil, err
				}
			}
			allow := tlsinbound.Config{AllowedSNI: cfg.AllowedSni}
			if name != "" && !allow.Allows(name) {
				return nil, errors.New("SNI not allowed")
			}
			route := snapshot.names[name]
			// A disabled/paused claimed name never falls through to a default rule.
			if route == nil && !snapshot.claimed[name] {
				route = snapshot.routes[cfg.DefaultRuleId]
			}
			if route == nil || route.l.ctx.Err() != nil || !route.account.acquire() {
				return nil, errors.New("route unavailable")
			}
			if !route.l.runtime.open() {
				route.account.active.Add(-1)
				return nil, errors.New("connection limit")
			}
			selected = route
			stopRoute = context.AfterFunc(route.l.ctx, func() { raw.Close() })
		}
		if selected.l.ctx.Err() != nil || !selected.account.valid() {
			return nil, errors.New("route unavailable")
		}
		if err := tlsinbound.ALPN(selected.l.rule.TlsAlpn); err != nil {
			return nil, err
		}
		child := &tls.Config{MinVersion: config.MinVersion, Certificates: []tls.Certificate{*snapshot.cert}, NextProtos: selected.l.rule.TlsAlpn, SessionTicketsDisabled: true}
		child.VerifyConnection = func(tls.ConnectionState) error {
			if selected.l.ctx.Err() != nil || !selected.account.valid() {
				return errors.New("route unavailable")
			}
			return nil
		}
		return child, nil
	}
	secured := tls.Server(client, config)
	if err = secured.HandshakeContext(handshakeCtx); err != nil || selected == nil {
		return
	}
	if !stopHandshake() || handshakeCtx.Err() != nil {
		return
	}
	cancelHandshake()
	releaseHandshake()
	_ = raw.SetDeadline(time.Time{})
	if selected.l.ctx.Err() != nil || !selected.account.valid() {
		return
	}
	e.forwardPrepared(selected.l, secured, addresses, selected.account, selected.count)
}
