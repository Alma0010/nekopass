package agent

import (
	"errors"
	"maps"
	"path/filepath"
	"testing"

	pb "github.com/nekopass/nekopass/internal/protocol"
	bolterrors "go.etcd.io/bbolt/errors"
)

func TestStateSkipsUnchangedHeartbeatWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state, err := OpenState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { state.Close() }()

	config := &pb.ControlMessage{Revision: 1, Node: &pb.NodeConfig{Enabled: true}}
	users := map[int64]DiskUser{7: {Metered: true, Epoch: 2, Issued: 100, Spent: 50, Charged: 10, Traffic: 10}}
	retired := map[string]DiskUser{"7:1": {Metered: true, Epoch: 1, Issued: 100, Spent: 50, Charged: 20, Traffic: 20}}
	rules := map[int64]int64{8: 30}

	writes := func() int64 {
		stats := state.db.Stats()
		return stats.TxStats.GetWrite()
	}
	save := func(t *testing.T) {
		t.Helper()
		if err := state.SaveConfig(config); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := func(t *testing.T) {
		t.Helper()
		if err := state.checkpoint(users, rules, retired); err != nil {
			t.Fatal(err)
		}
	}
	before := writes()
	if err := state.SaveConfig(&pb.ControlMessage{}); err != nil || writes() == before {
		t.Fatalf("first empty config did not persist: %v", err)
	}
	if pending, err := state.RestorePending(); err != nil || !pending {
		t.Fatalf("empty config cleared pending restore: pending=%v, err=%v", pending, err)
	}
	save(t)
	if pending, err := state.RestorePending(); err != nil || pending {
		t.Fatalf("node config did not clear pending restore: pending=%v, err=%v", pending, err)
	}
	checkpoint(t)

	const repeats = 10
	for _, test := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"config", save},
		{"checkpoint", checkpoint},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := writes()
			for range repeats {
				test.run(t)
			}
			delta := writes() - before
			t.Logf("%d bbolt writes for %d unchanged %s snapshots", delta, repeats, test.name)
			if delta != 0 {
				t.Errorf("unchanged state performed %d writes", delta)
			}
		})
	}

	config.Revision++
	users[7] = DiskUser{Metered: true, Epoch: 2, Issued: 100, Spent: 50, Charged: 15, Traffic: 15}
	retired["7:1"] = DiskUser{Metered: true, Epoch: 1, Issued: 100, Spent: 50, Charged: 25, Traffic: 25}
	rules[8] = 40
	retired[""] = retired["7:1"]
	if err := state.checkpoint(users, rules, retired); !errors.Is(err, bolterrors.ErrKeyRequired) {
		t.Fatalf("failed checkpoint did not return the database error: %v", err)
	}
	if got, err := state.LoadRules(); err != nil || got[8] != 30 {
		t.Fatalf("failed checkpoint changed rule counters: %v, err=%v", got, err)
	}
	delete(retired, "")
	save(t)
	checkpoint(t)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenState(path)
	if err != nil {
		t.Fatal(err)
	}
	state = reopened

	cached, err := state.Config()
	if err != nil || cached.GetRevision() != config.Revision {
		t.Fatalf("config revision after reopening: %d, err: %v", cached.GetRevision(), err)
	}
	gotUsers, err := state.LoadUsers()
	if err != nil || !maps.Equal(gotUsers, users) {
		t.Fatalf("current usage after reopening: %v, err: %v", gotUsers, err)
	}
	gotRetired, err := state.LoadRetired()
	if err != nil || !maps.Equal(gotRetired, retired) {
		t.Fatalf("retired usage after reopening: %v, err: %v", gotRetired, err)
	}
	gotRules, err := state.LoadRules()
	if err != nil || !maps.Equal(gotRules, rules) {
		t.Fatalf("rule usage after reopening: %v, err: %v", gotRules, err)
	}
}
