package control

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	pb "github.com/nekopass/nekopass/internal/protocol"
)

func TestPaddingConfigurationDistributionAndOldAgentGate(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	var entry, exit int64
	for _, id := range []*int64{&entry, &exit} {
		if err := f.p.QueryRow(ctx, "INSERT INTO nodes(name,token_hash,protocol_version) VALUES($1,$2,23) RETURNING id", Secret(), Hash(Secret())).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []int64{entry, exit} {
			for _, q := range []string{"DELETE FROM grants WHERE node_id=$1", "DELETE FROM rule_usage WHERE node_id=$1", "DELETE FROM rules WHERE node_id=$1 OR egress_node_id=$1", "DELETE FROM nodes WHERE id=$1"} {
				f.p.Exec(ctx, q, id)
			}
		}
	})
	for _, id := range []int64{entry, exit} {
		testAuthorize(t, f.p, f.user, id)
	}
	base := fmt.Sprintf("admin/nodes/%d/protocols", exit)
	w := f.request(f.admin, base, "GET", nil)
	securityStatus(t, w, 200)
	var details struct {
		Config NodeProtocolsInput `json:"config"`
	}
	json.Unmarshal(w.Body.Bytes(), &details)
	cfg := details.Config
	cfg.TunnelTransport = "raw_tcp"
	cfg.TunnelSecurity = "tls"
	cfg.TunnelProtocol = "tls_tcp"
	cfg.TunnelExitEnabled = true
	cfg.TunnelListenPort = 27443
	cfg.TunnelPublicHost = "exit.example.test"
	cfg.AllowedIngressIDs = []int64{entry}
	cfg.TLS.ServerName = "exit.example.test"
	cfg.TLS.PaddingEnabled = true
	cfg.TLS.PaddingScheme = []string{"stop=8", "0=30-30", "1=100-400", "2=500-1000,c,500-1000"}
	securityStatus(t, f.request(f.user, base, "PUT", cfg), 403)
	custom := cfg.TLS.PaddingScheme
	cfg.TLS.PaddingScheme = nil
	securityStatus(t, f.request(f.admin, base, "PUT", cfg), 200)
	defaulted, _, e := loadNodeInput(ctx, f.p, exit)
	if e != nil || len(defaulted.TLS.PaddingScheme) == 0 {
		t.Fatal("default policy was not explicitly saved for both peers", e)
	}
	cfg.TLS.PaddingScheme = custom
	securityStatus(t, f.request(f.admin, base, "PUT", cfg), 200)
	loaded, _, err := loadNodeInput(ctx, f.p, exit)
	if err != nil || !loaded.TLS.PaddingEnabled || !reflect.DeepEqual(loaded.TLS.PaddingScheme, cfg.TLS.PaddingScheme) {
		t.Fatal("padding configuration not saved", err)
	}
	cfg.TLS.PaddingScheme = []string{"stop=-1"}
	securityStatus(t, f.request(f.admin, base, "PUT", cfg), 400)
	cfg.TLS.PaddingScheme = loaded.TLS.PaddingScheme
	w = f.request(f.user, "rules", "POST", RuleInput{NodeID: entry, EgressNodeID: exit, Protocol: "tcp", Targets: []string{"127.0.0.1:8080"}, Enabled: true})
	securityStatus(t, w, 200)
	stream := &StreamServer{Server: f.s}
	instance := Secret()
	for _, version := range []int32{21, 22, 23, 24} {
		out, err := stream.exchange(ctx, f.p, entry, &pb.AgentMessage{ProtocolVersion: version, InstanceId: instance})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Rules) != 1 || out.Rules[0].Enabled != (version >= 23) || !out.Rules[0].Tls.PaddingEnabled || !reflect.DeepEqual(out.Rules[0].Tls.PaddingScheme, loaded.TLS.PaddingScheme) {
			t.Fatal("ingress capability gate or padding policy missing")
		}
	}
	exitInstance := Secret()
	for _, version := range []int32{21, 22, 23, 24} {
		out, err := stream.exchange(ctx, f.p, exit, &pb.AgentMessage{ProtocolVersion: version, InstanceId: exitInstance})
		if err != nil {
			t.Fatal(err)
		}
		if out.Node.TunnelExitEnabled != (version >= 23) || !out.Node.Tls.PaddingEnabled {
			t.Fatal("old exit accepted incompatible framing")
		}
	}
	// Preserve the optional settings when changing mode, but do not activate them.
	cfg.TunnelTransport = "h2"
	cfg.TunnelProtocol = "tls_h2"
	securityStatus(t, f.request(f.admin, base, "PUT", cfg), 200)
	out, err := stream.exchange(ctx, f.p, entry, &pb.AgentMessage{ProtocolVersion: 23, InstanceId: instance})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rules) != 1 || out.Rules[0].Tls.PaddingEnabled {
		t.Fatal("padding activated for h2")
	}
	loaded, _, err = loadNodeInput(ctx, f.p, exit)
	if err != nil || !loaded.TLS.PaddingEnabled {
		t.Fatal("hidden settings were erased", err)
	}
}
