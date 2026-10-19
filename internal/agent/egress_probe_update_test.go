package agent

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// Deterministically finish a probe after its representative grant is replaced.
// No network timing is needed to reproduce the update/completion interleaving.
func TestGroupProbeCompletionRespectsCurrentGrant(t *testing.T) {
	var queued atomic.Int64
	m := newEgressManager(nil, &queued)
	defer m.close()
	rule := &pb.Rule{Id: 1, UserId: 1, QuotaEpoch: 1, IngressNodeId: 1, EgressGroupId: 3, Enabled: true, Protocol: "tcp", HealthIntervalSeconds: 60, HealthFailures: 1, HealthSuccesses: 2,
		EgressPeers: []*pb.EgressPeer{{NodeId: 4, Weight: 1}, {NodeId: 5, Weight: 1, Standby: true}}}
	m.update([]*pb.Rule{rule})
	key := peerKey(peerRule(rule, rule.EgressPeers[0]))
	m.mu.Lock()
	oldHealth, oldRule := m.peers[key], m.peers[key].rule
	for _, h := range m.peers {
		h.next = time.Now().Add(time.Hour)
	}
	m.mu.Unlock()

	updated := proto.Clone(rule).(*pb.Rule)
	updated.Id, updated.UserId, updated.QuotaEpoch = 2, 2, 2
	m.update([]*pb.Rule{updated})
	m.mu.Lock()
	for _, h := range m.peers {
		h.next = time.Now().Add(time.Hour)
	}
	m.mu.Unlock()
	m.probeOutcome(key, oldHealth, oldRule, errors.New("old grant revoked"))
	chosen, release, err := m.choose(updated, nil)
	if err != nil || chosen.EgressNodeId != 4 {
		t.Fatal("stale probe disabled a healthy shared exit", err)
	}
	release()

	// Identical heartbeat snapshots must still accept observations; otherwise
	// long probes could be discarded on every control heartbeat indefinitely.
	m.mu.Lock()
	currentHealth, currentRule := m.peers[key], m.peers[key].rule
	m.mu.Unlock()
	m.update([]*pb.Rule{proto.Clone(updated).(*pb.Rule)})
	m.probeOutcome(key, currentHealth, currentRule, errors.New("real transport failure"))
	chosen, release, err = m.choose(updated, nil)
	if err != nil || chosen.EgressNodeId != 5 {
		t.Fatal("real failure was discarded across an identical heartbeat", err)
	}
	release()

	// Removing and re-adding an identical endpoint creates a new health object;
	// completions from the removed lifetime cannot alter its replacement.
	m.update(nil)
	m.update([]*pb.Rule{updated})
	m.probeOutcome(key, currentHealth, currentRule, errors.New("removed endpoint failure"))
	chosen, release, err = m.choose(updated, nil)
	if err != nil || chosen.EgressNodeId != 4 {
		t.Fatal("old endpoint lifetime poisoned its replacement", err)
	}
	release()
}
