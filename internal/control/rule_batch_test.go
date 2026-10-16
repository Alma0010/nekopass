package control

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/nekopass/nekopass/internal/tlsinbound"
)

func TestRuleBatchEditPermissionsPartialUpdatesAndRollback(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	var nodes []int64
	t.Cleanup(func() {
		for _, node := range nodes {
			for _, query := range []string{"DELETE FROM grants WHERE node_id=$1", "DELETE FROM rule_usage WHERE node_id=$1", "DELETE FROM rules WHERE node_id=$1", "DELETE FROM nodes WHERE id=$1"} {
				f.p.Exec(ctx, query, node)
			}
		}
	})
	newNode := func(authorized bool) int64 {
		t.Helper()
		var id int64
		if err := f.p.QueryRow(ctx, "INSERT INTO nodes(name,token_hash,protocol_version) VALUES($1,$2,21) RETURNING id", Secret(), Hash(Secret())).Scan(&id); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, id)
		if authorized {
			testAuthorize(t, f.p, f.user, id)
		}
		return id
	}
	entry, entry2, exit, forbidden := newNode(true), newNode(true), newNode(true), newNode(false)
	if _, err := f.p.Exec(ctx, "UPDATE nodes SET tunnel_exit_enabled=true,tunnel_listen_port=27001,tunnel_public_host='127.0.0.1' WHERE id=$1", exit); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{entry, entry2} {
		if _, err := f.p.Exec(ctx, "INSERT INTO node_tunnel_links(ingress_node_id,egress_node_id,token) VALUES($1,$2,$3)", id, exit, Secret()); err != nil {
			t.Fatal(err)
		}
	}
	create := func(protocol string, port int, name string) int64 {
		t.Helper()
		w := f.request(f.user, "rules", "POST", RuleInput{NodeID: entry, EgressNodeID: exit, Protocol: protocol, ListenPort: port, Name: name, Targets: []string{"127.0.0.1:8080"}, Balance: "random", Enabled: true})
		securityStatus(t, w, 200)
		var result struct{ ID int64 }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.ID
	}
	a, b, udp := create("tcp", 24101, "first"), create("tcp", 24102, "second"), create("udp", 24101, "datagram")
	if _, err := f.p.Exec(ctx, "UPDATE rules SET proxy_accept='v1',proxy_send='v1' WHERE id=$1", a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Exec(ctx, "UPDATE rule_usage SET traffic=12345 WHERE rule_id=$1", a); err != nil {
		t.Fatal(err)
	}
	get := func(id int64) RuleInput {
		t.Helper()
		tx, err := f.p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		in, err := loadRule(ctx, tx, id)
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	edit := func(actor int64, ids []int64, patch map[string]any, expected int) {
		t.Helper()
		securityStatus(t, f.request(actor, "rules/batch", "POST", map[string]any{"ids": ids, "action": "edit", "patch": patch}), expected)
	}
	beforeA, beforeB := get(a), get(b)
	edit(f.user, []int64{a, b}, map[string]any{"balance": "round_robin", "proxy_accept_enabled": false, "egress_node_id": 0}, 200)
	afterA, afterB := get(a), get(b)
	if afterA.Balance != "round_robin" || afterA.ProxyAccept != "off" || afterA.EgressNodeID != 0 || afterA.ListenPort != beforeA.ListenPort || afterA.ProxySend != "v1" || afterB.Enabled != beforeB.Enabled || afterA.Name != beforeA.Name || !reflect.DeepEqual(afterA.Targets, beforeA.Targets) {
		t.Fatal("partial update changed unrelated fields or lost zero/false")
	}
	var traffic int64
	if err := f.p.QueryRow(ctx, "SELECT traffic FROM rule_usage WHERE rule_id=$1", a).Scan(&traffic); err != nil || traffic != 12345 {
		t.Fatal("batch edit changed accounting", err)
	}
	edit(f.user, []int64{a, b}, map[string]any{"proxy_accept_enabled": true, "proxy_send_enabled": true}, 200)
	if get(a).ProxyAccept != "auto" || get(a).ProxySend != "v1" || get(b).ProxySend != "v2" {
		t.Fatal("proxy enable did not preserve existing sender version")
	}
	// A later invalid UDP child must roll back the already processed TCP child.
	beforeA = get(a)
	edit(f.user, []int64{a, udp}, map[string]any{"balance": "least_connections", "proxy_send_enabled": true}, 400)
	if !reflect.DeepEqual(beforeA, get(a)) {
		t.Fatal("invalid mixed-protocol batch partially saved")
	}
	edit(f.user, []int64{a, b}, map[string]any{"node_id": entry2, "egress_node_id": exit}, 200)
	if get(a).NodeID != entry2 || get(a).EgressNodeID != exit || get(b).NodeID != entry2 {
		t.Fatal("combined entry and exit update failed")
	}
	beforeA = get(a)
	edit(f.user, []int64{a}, map[string]any{"node_id": forbidden}, 400)
	edit(f.user, []int64{a}, map[string]any{"egress_node_id": forbidden}, 400)
	if !reflect.DeepEqual(beforeA, get(a)) {
		t.Fatal("unauthorized node update changed rule")
	}
	edit(f.user, []int64{a, b}, map[string]any{}, 400)
	edit(f.user, []int64{a, a}, map[string]any{"balance": "random"}, 400)
	edit(f.user, []int64{a}, map[string]any{"speed_mbps": 1000}, 400)
	edit(f.user, []int64{a}, map[string]any{"user_id": f.other}, 400)
	edit(f.user, []int64{a}, map[string]any{"balance": ""}, 400)
	edit(f.user, []int64{a}, map[string]any{"proxy_send_enabled": "false"}, 400)
	var foreign int64
	if err := f.p.QueryRow(ctx, "INSERT INTO rules(user_id,node_id,listen_port,target_host,target_port) VALUES($1,$2,24103,'127.0.0.1',8080) RETURNING id", f.other, entry).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	edit(f.user, []int64{a, foreign}, map[string]any{"balance": "random"}, 403)
	edit(f.admin, []int64{a}, map[string]any{"balance": "random"}, 403)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/users/%d/rules/batch", f.user), "POST", map[string]any{"ids": []int64{a, b}, "action": "edit", "patch": map[string]any{"balance": "least_connections"}}), 200)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/users/%d/rules/batch", f.user), "POST", map[string]any{"ids": []int64{a, foreign}, "action": "edit", "patch": map[string]any{"balance": "random"}}), 403)
	if get(a).Balance != "least_connections" {
		t.Fatal("managed user batch failed or cross-user batch partially saved")
	}
	// Same-node edits retain listener ports; moving to another entry allocates anew.
	port := get(a).ListenPort
	edit(f.user, []int64{a}, map[string]any{"node_id": entry2, "proxy_send_enabled": false}, 200)
	if get(a).ListenPort != port || get(a).ProxySend != "off" {
		t.Fatal("same-node update changed the port or ignored false")
	}
}

func TestRuleBatchEditSharedTLSPreservesSNIRouting(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	var node int64
	if err := f.p.QueryRow(ctx, "INSERT INTO nodes(name,token_hash,protocol_version) VALUES($1,$2,21) RETURNING id", Secret(), Hash(Secret())).Scan(&node); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{"DELETE FROM rule_usage WHERE node_id=$1", "DELETE FROM rules WHERE node_id=$1", "DELETE FROM nodes WHERE id=$1"} {
			f.p.Exec(ctx, query, node)
		}
	})
	testAuthorize(t, f.p, f.user, node)
	cfg := tlsinbound.DefaultConfig()
	cfg.Name = "batch shared"
	cfg.Port = 24443
	w := f.request(f.admin, fmt.Sprintf("admin/nodes/%d/tls-inbounds", node), "PUT", map[string]any{"config": cfg})
	securityStatus(t, w, 200)
	var inbound struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &inbound)
	w = f.request(f.user, "rules", "POST", RuleInput{NodeID: node, TLSInboundID: inbound.ID, SNINames: []string{"a.example.test"}, Protocol: "tcp", Targets: []string{"127.0.0.1:8443"}, Enabled: true})
	securityStatus(t, w, 200)
	var rule struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &rule)
	edit := func(patch map[string]any, expected int) {
		t.Helper()
		securityStatus(t, f.request(f.user, "rules/batch", "POST", map[string]any{"ids": []int64{rule.ID}, "action": "edit", "patch": patch}), expected)
	}
	edit(map[string]any{"balance": "round_robin", "proxy_send_enabled": true}, 200)
	edit(map[string]any{"proxy_accept_enabled": true}, 400)
	edit(map[string]any{"proxy_accept_enabled": false}, 400)
	edit(map[string]any{"node_id": node + 100000000}, 400)
	var sni []byte
	var shared int64
	var port int
	var balance string
	if err := f.p.QueryRow(ctx, "SELECT tls_inbound_id,listen_port,sni_names,balance FROM rules WHERE id=$1", rule.ID).Scan(&shared, &port, &sni, &balance); err != nil {
		t.Fatal(err)
	}
	if shared != inbound.ID || port != 0 || string(sni) != "[\"a.example.test\"]" || balance != "round_robin" {
		t.Fatal("batch changed shared TLS identity or failed atomic validation")
	}
}
