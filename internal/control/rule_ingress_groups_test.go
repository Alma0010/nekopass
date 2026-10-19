package control

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"testing"
)

func groupRuleFixture(t *testing.T, count int) (*securityFixture, int64, []int64) {
	t.Helper()
	f := newSecurityFixture(t)
	ctx := context.Background()
	nodes := []int64{}
	for i := 0; i < count; i++ {
		var node int64
		if err := f.p.QueryRow(ctx, `INSERT INTO nodes(name,token_hash,port_min,port_max,public_address,protocol_version,last_seen,applied_revision) VALUES($1,$2,$3,$4,$5,25,now(),1000000) RETURNING id`, Secret(), Secret(), 24000+i, 24010-i, fmt.Sprintf("entry%d.example.com", i)).Scan(&node); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	group := testAuthorize(t, f.p, f.user, nodes[0])
	for _, node := range nodes[1:] {
		if _, err := f.p.Exec(ctx, `INSERT INTO node_group_members(group_id,node_id) VALUES($1,$2)`, group, node); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		f.p.Exec(ctx, "DELETE FROM rules WHERE user_id=$1", f.user)
		for _, q := range []string{"DELETE FROM rule_events WHERE node_id=ANY($1)", "DELETE FROM usage_events WHERE node_id=ANY($1)", "DELETE FROM grants WHERE node_id=ANY($1)", "DELETE FROM rule_usage WHERE node_id=ANY($1)", "DELETE FROM nodes WHERE id=ANY($1)"} {
			f.p.Exec(ctx, q, nodes)
		}
	})
	return f, group, nodes
}
func groupInput(group int64) RuleInput {
	return RuleInput{IngressGroupID: group, Targets: []string{"127.0.0.1:8080"}, Enabled: true, Protocol: "tcp"}
}
func ruleResultID(t *testing.T, f *securityFixture, in RuleInput) int64 {
	t.Helper()
	w := f.request(f.user, "rules", "POST", in)
	securityStatus(t, w, 200)
	var v struct{ ID int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.ID
}
func groupRuleList(t *testing.T, f *securityFixture) []map[string]any {
	t.Helper()
	w := f.request(f.user, "rules", "GET", nil)
	securityStatus(t, w, 200)
	var rules []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestGroupRuleFansOutCountsOnceAndTracksMembers(t *testing.T) {
	f, group, nodes := groupRuleFixture(t, 6)
	ctx := context.Background()
	if _, err := f.p.Exec(ctx, "UPDATE users SET max_rules=1 WHERE id=$1", f.user); err != nil {
		t.Fatal(err)
	}
	id := ruleResultID(t, f, groupInput(group))
	var port, count int
	if err := f.p.QueryRow(ctx, "SELECT listen_port FROM rules WHERE id=$1", id).Scan(&port); err != nil {
		t.Fatal(err)
	}
	if port != 24005 {
		t.Fatalf("common port=%d", port)
	}
	f.p.QueryRow(ctx, "SELECT count(*) FROM rule_ports WHERE rule_id=$1", id).Scan(&count)
	if count != 6 {
		t.Fatalf("reserved %d ports", count)
	}
	securityStatus(t, f.request(f.user, "rules", "POST", groupInput(group)), 400)
	stream := &StreamServer{Server: f.s}
	instances := map[int64]string{}
	for _, node := range nodes {
		instances[node] = Secret()
		out, err := stream.exchange(ctx, f.p, node, &pb.AgentMessage{ProtocolVersion: 25, InstanceId: instances[node], AppliedRevision: 1000000})
		if err != nil || len(out.Rules) != 1 || out.Rules[0].Id != id || int(out.Rules[0].ListenPort) != port {
			t.Fatalf("fanout node %d: %v %v", node, out, err)
		}
	}
	rows := groupRuleList(t, f)
	if len(rows) != 1 || len(rows[0]["ingress_nodes"].([]any)) != 6 {
		t.Fatalf("not one logical group rule: %v", rows)
	}
	// Cumulative reports from different agents are accounted independently, then summed once.
	for i, node := range nodes[:2] {
		_, err := stream.exchange(ctx, f.p, node, &pb.AgentMessage{ProtocolVersion: 25, InstanceId: instances[node], AppliedRevision: 1000000, RuleUsage: []*pb.RuleUsage{{RuleId: id, Traffic: int64((i + 1) * 100)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := groupRuleList(t, f)[0]["traffic_bytes"].(float64); got != 300 {
		t.Fatalf("summed usage=%v", got)
	}
	// Removing members retires their listeners and ports, but retains historical traffic.
	kept := nodes[1:]
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": kept}), 200)
	out, err := stream.exchange(ctx, f.p, nodes[0], &pb.AgentMessage{ProtocolVersion: 25, InstanceId: instances[nodes[0]]})
	if err != nil || len(out.Rules) != 0 {
		t.Fatal("removed member still has rule", err)
	}
	f.p.QueryRow(ctx, "SELECT count(*) FROM rule_ports WHERE rule_id=$1", id).Scan(&count)
	if count != 5 {
		t.Fatalf("ports after removal=%d", count)
	}
	if got := groupRuleList(t, f)[0]["traffic_bytes"].(float64); got != 300 {
		t.Fatal("history lost", got)
	}
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes}), 200)
	out, err = stream.exchange(ctx, f.p, nodes[0], &pb.AgentMessage{ProtocolVersion: 25, InstanceId: instances[nodes[0]]})
	if err != nil || len(out.Rules) != 1 {
		t.Fatal("rejoined member missing rule", err)
	}
	// A mixed healthy/offline group remains one row and exposes each member's status.
	f.p.Exec(ctx, "UPDATE nodes SET last_seen=now()-interval '1 minute' WHERE id=$1", nodes[0])
	if got := groupRuleList(t, f)[0]["status"]; got != "partial" {
		t.Fatalf("mixed status=%v", got)
	}
}

func TestGroupRulePermissionsPortsAndAtomicMembership(t *testing.T) {
	f, group, nodes := groupRuleFixture(t, 2)
	ctx := context.Background()
	in := groupInput(group)
	bad := in
	bad.NodeID = nodes[0]
	securityStatus(t, f.request(f.user, "rules", "POST", bad), 400)
	securityStatus(t, f.request(f.other, "rules", "POST", in), 400)
	w := f.request(f.user, "rule-node-groups", "GET", nil)
	securityStatus(t, w, 200)
	var choices []map[string]any
	json.Unmarshal(w.Body.Bytes(), &choices)
	if len(choices) != 1 || len(choices[0]["members"].([]any)) != 2 {
		t.Fatal("group choices missing members")
	}
	w = f.request(f.other, "rule-node-groups", "GET", nil)
	securityStatus(t, w, 200)
	if w.Body.String() != "[]\n" {
		t.Fatal("foreign groups exposed", w.Body.String())
	}
	in.ListenPort = 24001
	id := ruleResultID(t, f, in)
	conflict := RuleInput{NodeID: nodes[1], Targets: in.Targets, Protocol: "tcp", ListenPort: 24001, Enabled: true}
	securityStatus(t, f.request(f.user, "rules", "POST", conflict), 400)
	conflict.Protocol = "udp"
	ruleResultID(t, f, conflict)
	// The same port on a new member must reject and roll back the entire group edit.
	var extra int64
	f.p.QueryRow(ctx, "INSERT INTO nodes(name,token_hash) VALUES($1,$2) RETURNING id", Secret(), Secret()).Scan(&extra)
	t.Cleanup(func() {
		f.p.Exec(ctx, "DELETE FROM rules WHERE node_id=$1", extra)
		f.p.Exec(ctx, "DELETE FROM rule_usage WHERE node_id=$1", extra)
		f.p.Exec(ctx, "DELETE FROM nodes WHERE id=$1", extra)
	})
	testAuthorize(t, f.p, f.user, extra)
	ruleResultID(t, f, RuleInput{NodeID: extra, Targets: in.Targets, ListenPort: 24001, Enabled: true})
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": append(append([]int64{}, nodes...), extra)}), 409)
	var count int
	f.p.QueryRow(ctx, "SELECT count(*) FROM node_group_members WHERE group_id=$1", group).Scan(&count)
	if count != 2 {
		t.Fatal("partial membership committed", count)
	}
	// Disabling the bound group cannot be bypassed through another group on the same nodes.
	testAuthorize(t, f.p, f.user, nodes[0])
	testAuthorize(t, f.p, f.user, nodes[1])
	f.p.Exec(ctx, "UPDATE node_groups SET enabled=false WHERE id=$1", group)
	out, err := (&StreamServer{Server: f.s}).exchange(ctx, f.p, nodes[0], &pb.AgentMessage{ProtocolVersion: 25, InstanceId: Secret()})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range out.Rules {
		if rule.Id == id {
			t.Fatal("disabled binding bypassed via another group")
		}
	}
	f.p.Exec(ctx, "UPDATE node_groups SET enabled=true WHERE id=$1", group)
	f.p.Exec(ctx, "UPDATE nodes SET allow_direct=false WHERE id=$1", nodes[1])
	securityStatus(t, f.request(f.user, fmt.Sprintf("rules/%d", id), "PUT", in), 400)
	// Restore the fixture after intentionally inconsistent direct SQL changes.
	f.p.Exec(ctx, "UPDATE nodes SET allow_direct=true WHERE id=$1", nodes[1])
}

func TestGroupTunnelDispatchSupportsEveryIngressAndRequiresNewExitAgent(t *testing.T) {
	f, group, nodes := groupRuleFixture(t, 2)
	ctx := context.Background()
	var exit int64
	if err := f.p.QueryRow(ctx, `INSERT INTO nodes(name,token_hash,ingress_enabled,tunnel_exit_enabled,tunnel_public_host,tunnel_listen_port,protocol_version) VALUES($1,$2,false,true,'127.0.0.1',35000,25) RETURNING id`, Secret(), Secret()).Scan(&exit); err != nil {
		t.Fatal(err)
	}
	testAuthorize(t, f.p, f.user, exit)
	t.Cleanup(func() {
		f.p.Exec(ctx, "DELETE FROM rules WHERE user_id=$1", f.user)
		f.p.Exec(ctx, "DELETE FROM grants WHERE node_id=$1", exit)
		f.p.Exec(ctx, "DELETE FROM nodes WHERE id=$1", exit)
	})
	in := groupInput(group)
	in.EgressNodeID = exit
	for _, node := range nodes {
		if _, err := f.p.Exec(ctx, "INSERT INTO node_tunnel_links(ingress_node_id,egress_node_id,token) VALUES($1,$2,$3)", node, exit, Secret()); err != nil {
			t.Fatal(err)
		}
	}
	id := ruleResultID(t, f, in)
	stream := &StreamServer{Server: f.s}
	instance := Secret()
	out, err := stream.exchange(ctx, f.p, exit, &pb.AgentMessage{ProtocolVersion: 25, InstanceId: instance})
	if err != nil || len(out.EgressRules) != 2 {
		t.Fatal("exit must authorize both ingresses", out, err)
	}
	for _, r := range out.EgressRules {
		if r.RuleId != id {
			t.Fatal("different logical rule")
		}
	}
	out, err = stream.exchange(ctx, f.p, exit, &pb.AgentMessage{ProtocolVersion: 24, InstanceId: instance})
	if err != nil || len(out.EgressRules) != 0 {
		t.Fatal("old exit received group routes", err)
	}
	out, err = stream.exchange(ctx, f.p, nodes[0], &pb.AgentMessage{ProtocolVersion: 25, InstanceId: Secret()})
	if err != nil || len(out.Rules) != 1 || out.Rules[0].Enabled {
		t.Fatal("group tunnel active before exit upgrade", err)
	}
}

func TestGroupBatchAndImportKeepBindingsAndRollbackAtomically(t *testing.T) {
	f, group, nodes := groupRuleFixture(t, 2)
	ctx := context.Background()
	singleton := ruleResultID(t, f, RuleInput{NodeID: nodes[0], Targets: []string{"127.0.0.1:8080"}, Enabled: true})
	patch := &RuleBatchPatch{IngressGroupID: &group}
	securityStatus(t, f.request(f.user, "rules/batch", "POST", BatchInput{IDs: []int64{singleton}, Action: "edit", Patch: patch}), 200)
	var bound int64
	var count int
	f.p.QueryRow(ctx, "SELECT ingress_group_id FROM rules WHERE id=$1", singleton).Scan(&bound)
	if bound != group {
		t.Fatal("batch lost group binding")
	}
	rows := []RuleInput{groupInput(group), groupInput(group)}
	rows[1].IngressGroupID = 0
	rows[1].NodeID = 0
	securityStatus(t, f.request(f.user, "rules/import", "POST", map[string]any{"rules": rows}), 400)
	f.p.QueryRow(ctx, "SELECT count(*) FROM rules WHERE user_id=$1", f.user).Scan(&count)
	if count != 1 {
		t.Fatal("partial import persisted", count)
	}
	rows[1] = groupInput(group)
	securityStatus(t, f.request(f.user, "rules/import", "POST", map[string]any{"rules": rows}), 200)
	listed := groupRuleList(t, f)
	if len(listed) != 3 {
		t.Fatal("group import fanned out database rows", listed)
	}
	for _, r := range listed {
		if r["ingress_group_id"].(float64) != float64(group) {
			t.Fatal("group import binding lost")
		}
	}
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/users/%d/rule-node-groups", f.user), "GET", nil), 200)
	securityStatus(t, f.request(f.other, fmt.Sprintf("admin/users/%d/rule-node-groups", f.user), "GET", nil), 403)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": []int64{}}), 200)
	listed = groupRuleList(t, f)
	if len(listed) != 3 || len(listed[0]["ingress_nodes"].([]any)) != 0 || listed[0]["status"] != "unauthorized" {
		t.Fatal("empty group rules disappeared or appear active", listed)
	}
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", group), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": nodes}), 200)
	if len(groupRuleList(t, f)[0]["ingress_nodes"].([]any)) != 2 {
		t.Fatal("group did not recover after adding members")
	}
}

func TestIngressAndEgressGroupsKeepEachEntrysOwnRoutes(t *testing.T) {
	f, ingressGroup, entries := groupRuleFixture(t, 2)
	ctx := context.Background()
	var egressGroup int64
	if err := f.p.QueryRow(ctx, "INSERT INTO node_groups(name) VALUES($1) RETURNING id", Secret()).Scan(&egressGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Exec(ctx, "INSERT INTO user_node_groups(user_id,group_id) VALUES($1,$2)", f.user, egressGroup); err != nil {
		t.Fatal(err)
	}
	exits := []int64{}
	for i, entry := range entries {
		var exit int64
		if err := f.p.QueryRow(ctx, `INSERT INTO nodes(name,token_hash,ingress_enabled,tunnel_exit_enabled,tunnel_public_host,tunnel_listen_port,protocol_version) VALUES($1,$2,false,true,'127.0.0.1',35000,25) RETURNING id`, Secret(), Secret()).Scan(&exit); err != nil {
			t.Fatal(err)
		}
		exits = append(exits, exit)
		if _, err := f.p.Exec(ctx, "INSERT INTO node_group_members(group_id,node_id,role) VALUES($1,$2,$3)", egressGroup, exit, []string{"active", "standby"}[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := f.p.Exec(ctx, "INSERT INTO node_tunnel_links(ingress_node_id,egress_node_id,token) VALUES($1,$2,$3)", entry, exit, Secret()); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		f.p.Exec(ctx, "DELETE FROM rules WHERE user_id=$1", f.user)
		f.p.Exec(ctx, "DELETE FROM user_node_groups WHERE group_id=$1", egressGroup)
		f.p.Exec(ctx, "DELETE FROM node_groups WHERE id=$1", egressGroup)
		f.p.Exec(ctx, "DELETE FROM grants WHERE node_id=ANY($1)", exits)
		f.p.Exec(ctx, "DELETE FROM nodes WHERE id=ANY($1)", exits)
	})
	in := groupInput(ingressGroup)
	in.EgressGroupID = egressGroup
	ruleID := ruleResultID(t, f, in)
	stream := &StreamServer{Server: f.s}
	instances := map[int64]string{}
	exchange := func(node int64, version int32) *pb.ControlMessage {
		t.Helper()
		if instances[node] == "" {
			instances[node] = Secret()
		}
		out, err := stream.exchange(ctx, f.p, node, &pb.AgentMessage{ProtocolVersion: version, InstanceId: instances[node]})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for i, entry := range entries {
		out := exchange(entry, 25)
		if len(out.Rules) != 1 || !out.Rules[0].Enabled || len(out.Rules[0].EgressPeers) != 1 || out.Rules[0].EgressPeers[0].NodeId != exits[i] {
			t.Fatalf("entry %d received wrong routes: %v", entry, out.Rules)
		}
		exit := exchange(exits[i], 25)
		if len(exit.EgressRules) != 1 || exit.EgressRules[0].RuleId != ruleID || exit.EgressRules[0].IngressNodeId != entry {
			t.Fatalf("exit received foreign ingress authorization: %v", exit.EgressRules)
		}
	}
	if exchange(entries[0], 24).Rules[0].Enabled {
		t.Fatal("a legacy v24 entry received an enabled group rule")
	}
	if len(exchange(exits[0], 24).EgressRules) != 0 {
		t.Fatal("a legacy v24 exit received group grants")
	}
	if exchange(entries[0], 25).Rules[0].Enabled {
		t.Fatal("entry stayed active with an incompatible exit")
	}
	if !exchange(entries[1], 25).Rules[0].Enabled {
		t.Fatal("other ingress was affected by incompatible exit")
	}
	exchange(exits[0], 25)
	// Member roles apply to ingress bindings as well as outbound scheduling.
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/node-groups/%d", ingressGroup), "PUT", map[string]any{"name": Secret(), "enabled": true, "node_ids": entries, "members": []nodeGroupMember{{NodeID: entries[0], Role: "disabled", Weight: 1}, {NodeID: entries[1], Role: "active", Weight: 1}}}), 200)
	if len(exchange(entries[0], 25).Rules) != 0 || len(exchange(entries[1], 25).Rules) != 1 {
		t.Fatal("disabled ingress member still receives rule")
	}
}
