package control

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"github.com/nekopass/nekopass/internal/tlsinbound"
	"strings"
	"testing"
)

func TestTLSInboundManagementPermissionsClaimsAndCompatibility(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	var node int64
	if err := f.p.QueryRow(ctx, "INSERT INTO nodes(name,token_hash,protocol_version,last_seen) VALUES($1,$2,20,now()) RETURNING id", Secret(), Hash(Secret())).Scan(&node); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{"DELETE FROM grants WHERE node_id=$1", "DELETE FROM rule_usage WHERE node_id=$1", "DELETE FROM rules WHERE node_id=$1", "DELETE FROM nodes WHERE id=$1"} {
			f.p.Exec(ctx, query, node)
		}
	})
	for _, user := range []int64{f.user, f.other, f.admin} {
		testAuthorize(t, f.p, user, node)
	}
	path := fmt.Sprintf("admin/nodes/%d/tls-inbounds", node)
	cfg := tlsinbound.DefaultConfig()
	cfg.Name = "Shared TLS"
	cfg.Port = 24443
	cfg.TLS.ServerName = "*.example.test"
	cfg.AllowedSNI = []string{"example.test"}
	securityStatus(t, f.request(f.user, path, "GET", nil), 403)
	securityStatus(t, f.request(f.user, path, "PUT", map[string]any{"config": cfg}), 403)
	response := f.request(f.admin, path, "PUT", map[string]any{"config": cfg})
	if response.Code != 200 {
		t.Fatalf("resource creation failed: %d %s", response.Code, response.Body.String())
	}
	var resource struct{ ID int64 }
	if err := json.Unmarshal(response.Body.Bytes(), &resource); err != nil {
		t.Fatal(err)
	}
	accountCfg := DefaultTunnelTLS()
	accountCfg.ACMEEmail = "fixture@example.test"
	nodeAccount, err := f.s.acmeAccount(ctx, node, accountCfg)
	if err != nil {
		t.Fatal(err)
	}
	inboundAccount, err := f.s.acmeResourceAccount(ctx, node, resource.ID, accountCfg)
	if err != nil {
		t.Fatal(err)
	}
	keyA, _ := x509.MarshalPKCS8PrivateKey(nodeAccount.key)
	keyB, _ := x509.MarshalPKCS8PrivateKey(inboundAccount.key)
	if string(keyA) == string(keyB) {
		t.Fatal("entry and exit ACME accounts were conflated")
	}
	reloaded, err := f.s.acmeResourceAccount(ctx, node, resource.ID, accountCfg)
	if err != nil {
		t.Fatal(err)
	}
	keyC, _ := x509.MarshalPKCS8PrivateKey(reloaded.key)
	if string(keyB) != string(keyC) {
		t.Fatal("inbound ACME account was not reused")
	}
	choices := f.request(f.user, "tls-inbounds", "GET", nil)
	securityStatus(t, choices, 200)
	for _, secret := range []string{"PRIVATE KEY", "BEGIN CERTIFICATE", "dns_credentials", "private_key"} {
		if strings.Contains(choices.Body.String(), secret) {
			t.Fatal("certificate credentials exposed to users")
		}
	}
	create := func(actor int64, in RuleInput) int64 {
		t.Helper()
		w := f.request(actor, "rules", "POST", in)
		if w.Code != 200 {
			t.Fatalf("rule creation failed: %d %s", w.Code, w.Body.String())
		}
		var result struct{ ID int64 }
		json.Unmarshal(w.Body.Bytes(), &result)
		return result.ID
	}
	input := RuleInput{NodeID: node, Protocol: "tcp", TLSInboundID: resource.ID, SNINames: []string{"A.Example.Test."}, Targets: []string{"127.0.0.1:8080"}, Enabled: true}
	rule := create(f.user, input)
	input.SNINames = []string{"b.example.test"}
	otherRule := create(f.other, input)
	input.SNINames = []string{"a.example.test"}
	securityStatus(t, f.request(f.other, "rules", "POST", input), 400)
	securityStatus(t, f.request(f.user, "rules/batch", "POST", BatchInput{IDs: []int64{rule}, Action: "disable"}), 200)
	securityStatus(t, f.request(f.other, "rules", "POST", input), 400)
	input.Protocol = "udp"
	securityStatus(t, f.request(f.user, "rules", "POST", input), 400)
	input.Protocol = "tcp"
	input.SNINames = []string{"deep.a.example.test"}
	securityStatus(t, f.request(f.user, "rules", "POST", input), 400)
	input.SNINames = []string{"outside.invalid"}
	securityStatus(t, f.request(f.user, "rules", "POST", input), 400)
	securityStatus(t, f.request(f.admin, path, "DELETE", map[string]any{"id": resource.ID}), 409)
	// The listener owns the port even though child rule rows contain port zero.
	plain := RuleInput{NodeID: node, Protocol: "tcp", ListenPort: 24443, Targets: []string{"127.0.0.1:8080"}, Enabled: true}
	securityStatus(t, f.request(f.user, "rules", "POST", plain), 400)
	// A user cannot operate another user's shared child.
	securityStatus(t, f.request(f.user, "rules/batch", "POST", BatchInput{IDs: []int64{otherRule}, Action: "disable"}), 403)
	// Only an administrator's own rule can receive unknown SNI traffic.
	cfg.UnknownSNIAction = "default_rule"
	cfg.DefaultRuleID = otherRule
	securityStatus(t, f.request(f.admin, path, "PUT", map[string]any{"id": resource.ID, "config": cfg}), 400)
	cfg.UnknownSNIAction = "reject"
	cfg.DefaultRuleID = 0
	cfg.TLS.ServerName = "different.example.test"
	securityStatus(t, f.request(f.admin, path, "PUT", map[string]any{"id": resource.ID, "config": cfg}), 409)
	cfg.TLS.ServerName = "*.example.test"
	cfg.Enabled = false
	securityStatus(t, f.request(f.admin, path, "PUT", map[string]any{"id": resource.ID, "config": cfg}), 200)
	cfg.Enabled = true
	securityStatus(t, f.request(f.admin, path, "PUT", map[string]any{"id": resource.ID, "config": cfg}), 200)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/users/%d/rules/batch", f.user), "POST", BatchInput{IDs: []int64{rule}, Action: "enable"}), 200)
	// Editing and loading keeps SNI/origin fields, and ordinary users see the shared port.
	tx, err := f.p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRule(ctx, tx, rule)
	tx.Rollback(ctx)
	if err != nil || loaded.ListenPort != 0 || loaded.SNINames[0] != "a.example.test" {
		t.Fatal("shared rule not preserved", err)
	}
	list := f.request(f.user, "rules", "GET", nil)
	securityStatus(t, list, 200)
	var rows []map[string]any
	if err = json.Unmarshal(list.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["listen_port"] != float64(24443) {
		t.Fatal("user-facing shared port not resolved")
	}
	stream := &StreamServer{Server: f.s}
	instance := Secret()
	for _, version := range []int32{19, 20} {
		out, err := stream.exchange(ctx, f.p, node, &pb.AgentMessage{InstanceId: instance, ProtocolVersion: version})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range out.Rules {
			if r.Id == rule {
				found = true
				if r.Enabled != (version >= 20) || r.TlsInboundId != resource.ID || len(r.SniNames) != 1 {
					t.Fatal("old agent forwarding not blocked")
				}
			}
		}
		if !found || len(out.Node.TlsInbounds) != 1 {
			t.Fatal("shared configuration not delivered")
		}
	}
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/nodes/%d/tls-policy", node), "PUT", map[string]bool{"tls_only": true}), 200)
	plain.ListenPort = 24444
	securityStatus(t, f.request(f.user, "rules", "POST", plain), 400)
	// Existing node deletion protection still applies while rules use the node.
	if _, err = f.p.Exec(ctx, "DELETE FROM nodes WHERE id=$1", node); err == nil {
		t.Fatal("node with active rules was deleted")
	}
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/users/%d/rules/batch", f.user), "POST", BatchInput{IDs: []int64{rule}, Action: "delete"}), 200)
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/users/%d/rules/batch", f.other), "POST", BatchInput{IDs: []int64{otherRule}, Action: "delete"}), 200)
	securityStatus(t, f.request(f.admin, path, "DELETE", map[string]any{"id": resource.ID}), 200)
	// Once resources are removed their port and SNI reservations must disappear.
	securityStatus(t, f.request(f.admin, fmt.Sprintf("admin/nodes/%d", node), "DELETE", nil), 409)
	var claims int
	if err = f.p.QueryRow(ctx, "SELECT count(*) FROM rule_ports WHERE node_id=$1", node).Scan(&claims); err != nil || claims != 0 {
		t.Fatal("shared listener port reservation leaked", err)
	}
}

func TestTLSInboundACMEAccountAndChallengeIsolation(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	tx, err := f.p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var node, inbound int64
	if err = tx.QueryRow(ctx, "INSERT INTO nodes(name,token_hash) VALUES($1,$2) RETURNING id", Secret(), Hash(Secret())).Scan(&node); err != nil {
		t.Fatal(err)
	}
	cfg := tlsinbound.DefaultConfig()
	cfg.Name = "ACME shared"
	cfg.Port = 24443
	cfg.TLS.ServerName = "a.example.test"
	cfg.TLS.CertificateMode = "acme_http"
	raw, _ := json.Marshal(cfg)
	if err = tx.QueryRow(ctx, "INSERT INTO node_tls_inbounds(node_id,port,config) VALUES($1,$2,$3) RETURNING id", node, cfg.Port, raw).Scan(&inbound); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO node_acme_challenges(node_id,domain,token,key_authorization,revision,expires_at,inbound_id) VALUES($1,'a.example.test','fixture-inbound','fixture-key',2,now()+interval '10 minutes',$2)", node, inbound); err != nil {
		t.Fatal(err)
	}
	out := &pb.ControlMessage{Node: &pb.NodeConfig{}}
	if err = appendTLSControl(ctx, tx, node, out); err != nil {
		t.Fatal(err)
	}
	if len(out.Node.Tls.Challenges) != 0 {
		t.Fatal("inbound HTTP challenge leaked into exit certificate listener")
	}
	if err = appendInboundChallenges(ctx, tx, node, out); err != nil {
		t.Fatal(err)
	}
	if len(out.Node.AcmeHttp) != 1 || out.Node.AcmeHttp[0].Port != 80 || out.Node.AcmeHttp[0].Challenges[0].Token != "fixture-inbound" {
		t.Fatal("inbound HTTP challenge not scoped")
	}
}
