package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/protobuf/proto"
)

func TestUnsyncedRuleAuthorizationDoesNotBlockSharedEgress(t *testing.T) {
	target, _ := inboundEcho(t, false)
	exit, h2, exitConfig, authorizedRule := h2Fixture(t, target)
	if err := exit.Apply(exitConfig); err != nil {
		t.Fatal(err)
	}
	var queued atomic.Int64
	m := newEgressManager(h2, &queued)
	defer m.close()
	peer := &pb.EgressPeer{NodeId: 3, Host: authorizedRule.TunnelHost, Port: authorizedRule.TunnelPort, Token: authorizedRule.TunnelToken, Protocol: authorizedRule.TunnelProtocol, Tls: authorizedRule.Tls, Weight: 1}
	ruleB := proto.Clone(authorizedRule).(*pb.Rule)
	ruleB.Enabled = true
	ruleB.EgressGroupId = 55
	ruleB.EgressStrategy = "round_robin"
	ruleB.HealthIntervalSeconds = 60
	ruleB.HealthFailures = 1
	ruleB.HealthSuccesses = 2
	ruleB.EgressPeers = []*pb.EgressPeer{peer}
	ruleA := proto.Clone(ruleB).(*pb.Rule)
	ruleA.Id = ruleB.Id + 1

	// The exit has a valid tunnel link and an authorization grant for B only.
	// A and B share the same group and tunnel peer key, as in a staggered
	// control-plane update where A has reached ingress before its exit grant.
	m.update([]*pb.Rule{ruleB})
	key := peerKey(peerRule(ruleB, peer))
	m.mu.Lock()
	m.peers[key].next = time.Now().Add(time.Minute)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, release, err := m.dial(ctx, ctx, ruleB, target, false)
	cancel()
	if err != nil {
		t.Fatal("control rule B should work before the unsynced A attempt:", err)
	}
	conn.Close()
	release()

	m.update([]*pb.Rule{ruleA, ruleB})
	m.mu.Lock()
	representative := m.peers[key].rule.Id
	m.peers[key].next = time.Now().Add(time.Minute)
	m.mu.Unlock()
	if representative != ruleB.Id {
		t.Fatalf("adding unsynchronized rule A replaced still-valid probe representative B: got rule %d", representative)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	_, release, err = m.dial(ctx, ctx, ruleA, target, false)
	cancel()
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("rule A unexpectedly passed without an exit authorization grant")
	}
	m.mu.Lock()
	blockedAfterA := m.schedules[scheduleKey(ruleA)].health[key].blocked
	m.mu.Unlock()

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	conn, release, err = m.dial(ctx, ctx, ruleB, target, false)
	cancel()
	if blockedAfterA || err != nil {
		t.Fatalf("unsynchronized rule A polluted the shared egress: blocked=%t; previously working rule B dial error=%v", blockedAfterA, err)
	}
	conn.Close()
	release()

	// Once B leaves the active set, the representative should follow the
	// remaining rule instead of retaining a removed rule's credentials.
	m.update([]*pb.Rule{ruleA})
	m.mu.Lock()
	representative = m.peers[key].rule.Id
	m.mu.Unlock()
	if representative != ruleA.Id {
		t.Fatalf("probe representative did not leave removed rule B: got rule %d", representative)
	}
}

func TestGroupWeightedRandomExcludesStandby(t *testing.T) {
	h2 := newH2Transport()
	defer h2.Close()
	var queued atomic.Int64
	m := newEgressManager(h2, &queued)
	defer m.close()
	r := &pb.Rule{Id: 1, Enabled: true, EgressGroupId: 56, Protocol: "tcp", EgressStrategy: "random", HealthIntervalSeconds: 60,
		EgressPeers: []*pb.EgressPeer{{NodeId: 1, Weight: 3}, {NodeId: 2, Weight: 1}, {NodeId: 3, Weight: 100, Standby: true}}}
	m.update([]*pb.Rule{r})
	counts := map[int64]int{}
	for i := 0; i < 10000; i++ {
		peer, release, err := m.choose(r, nil)
		if err != nil {
			t.Fatal(err)
		}
		counts[peer.EgressNodeId]++
		release()
	}
	if counts[3] != 0 {
		t.Fatalf("standby peer participated while primaries were healthy: %#v", counts)
	}
	share := float64(counts[1]) / float64(counts[1]+counts[2])
	if share < 0.70 || share > 0.80 {
		t.Fatalf("random selection ignored the 3:1 active weights: counts=%#v share=%.3f", counts, share)
	}
}
