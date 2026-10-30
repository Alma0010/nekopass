package control

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"github.com/nekopass/nekopass/internal/tlsinbound"
	"testing"
)

func TestGroupTLSFanoutClaimsAndMembership(t *testing.T) {
	f, group, nodes := groupRuleFixture(t, 2)
	ctx := context.Background()
	ids := map[int64]int64{}
	for _, node := range nodes {
		cfg := tlsinbound.DefaultConfig()
		cfg.Name = "group SNI"
		cfg.Port = 24443
		cfg.AllowedSNI = []string{"example.test"}
		w := f.request(f.admin, fmt.Sprintf("admin/nodes/%d/tls-inbounds", node), "PUT", map[string]any{"config": cfg})
		if w.Code != 200 {
			t.Fatalf("listener: %d %s", w.Code, w.Body.String())
		}
		var result struct{ ID int64 }
		json.Unmarshal(w.Body.Bytes(), &result)
		ids[node] = result.ID
	}
	in := groupInput(group)
	in.TLSInboundPort = 24443
	in.SNINames = []string{"app.example.test"}
	rule := ruleResultID(t, f, in)
	var claims int
	if err := f.p.QueryRow(ctx, "SELECT count(*) FROM rule_sni_names WHERE rule_id=$1", rule).Scan(&claims); err != nil || claims != 2 {
		t.Fatal("missing per-node names", claims, err)
	}
	for _, node := range nodes {
		out, err := (&StreamServer{Server: f.s}).exchange(ctx, f.p, node, &pb.AgentMessage{ProtocolVersion: pb.CurrentVersion, InstanceId: Secret()})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Rules) != 1 || out.Rules[0].TlsInboundId != ids[node] || len(out.Rules[0].SniNames) != 1 {
			t.Fatalf("wrong per-node inbound: %v", out.Rules)
		}
	}
	// Shared names conflict with a single-node rule too; paused rules retain claims.
	securityStatus(t, f.request(f.user, "rules/batch", "POST", BatchInput{IDs: []int64{rule}, Action: "disable"}), 200)
	single := in
	single.IngressGroupID = 0
	single.NodeID = nodes[1]
	single.TLSInboundPort = 0
	single.TLSInboundID = ids[nodes[1]]
	securityStatus(t, f.request(f.user, "rules", "POST", single), 400)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/nodes/%d/tls-inbounds", nodes[1]), "DELETE", map[string]any{"id": ids[nodes[1]]}), 409)
	// A port missing from any member cannot produce a partially installed group rule.
	in.TLSInboundPort = 24444
	in.SNINames = []string{"other.example.test"}
	securityStatus(t, f.request(f.user, "rules", "POST", in), 400)
	rows := groupRuleList(t, f)
	if len(rows) != 1 || rows[0]["listen_port"] != float64(24443) || rows[0]["tls_inbound_port"] != float64(24443) {
		t.Fatal("group TLS did not round-trip", rows)
	}
	// Removing the first member doesn't strand a representative inbound ID.
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes[1:]}), 200)
	if err := f.p.QueryRow(ctx, "SELECT count(*) FROM rule_sni_names WHERE rule_id=$1", rule).Scan(&claims); err != nil || claims != 1 {
		t.Fatal("removed member kept names", claims, err)
	}
	// A newly added member must not steal names registered after it left.
	testAuthorize(t, f.p, f.user, nodes[0])
	single.NodeID = nodes[0]
	single.TLSInboundID = ids[nodes[0]]
	conflict := ruleResultID(t, f, single)
	w := f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes})
	securityStatus(t, w, 409)
	var members int
	if err := f.p.QueryRow(ctx, "SELECT count(*) FROM node_group_members WHERE group_id=$1", group).Scan(&members); err != nil || members != 1 {
		t.Fatal("conflicting member edit partially saved", members, err)
	}
	securityStatus(t, f.request(f.user, "rules/batch", "POST", BatchInput{IDs: []int64{conflict}, Action: "delete"}), 200)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes}), 200)
	if err := f.p.QueryRow(ctx, "SELECT count(*) FROM rule_sni_names WHERE rule_id=$1", rule).Scan(&claims); err != nil || claims != 2 {
		t.Fatal("new member missing SNI claim", claims, err)
	}
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes[1:]}), 200)
	// Re-adding a member whose listener disappeared fails atomically.
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/nodes/%d/tls-inbounds", nodes[0]), "DELETE", map[string]any{"id": ids[nodes[0]]}), 200)
	w = f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes})
	if w.Code == 200 {
		t.Fatal("member without shared listener accepted")
	}
	if err := f.p.QueryRow(ctx, "SELECT count(*) FROM node_group_members WHERE group_id=$1", group).Scan(&members); err != nil || members != 1 {
		t.Fatal("failed membership edit was partially saved", members, err)
	}
	// Listener edits cannot move a group away from its configured shared port.
	cfg := tlsinbound.DefaultConfig()
	cfg.Name = "moved"
	cfg.Port = 24445
	w = f.request(f.admin, fmt.Sprintf("admin/nodes/%d/tls-inbounds", nodes[1]), "PUT", map[string]any{"id": ids[nodes[1]], "config": cfg})
	if w.Code == 200 {
		t.Fatal("listener moved while referenced by a group")
	}
	var port int
	if err := f.p.QueryRow(ctx, "SELECT port FROM node_tls_inbounds WHERE id=$1", ids[nodes[1]]).Scan(&port); err != nil || port != 24443 {
		t.Fatal("failed listener edit not rolled back", port, err)
	}
}
