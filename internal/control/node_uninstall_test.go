package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestNodeUninstallRequiresTargetAgentAndCompletion(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	var target, sibling int64
	targetToken, siblingToken := Secret(), Secret()
	targetInstance, siblingInstance := Secret(), Secret()
	for _, node := range []struct {
		id              *int64
		token, instance string
	}{
		{&target, targetToken, targetInstance},
		{&sibling, siblingToken, siblingInstance},
	} {
		if err := f.p.QueryRow(ctx, `INSERT INTO nodes(name,token_hash,instance_id,enabled,last_seen,config_revision,uninstall_supported)
 VALUES($1,$2,$3,false,now()-interval '1 hour',10,true) RETURNING id`, Secret(), Hash(node.token), node.instance).Scan(node.id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = f.p.Exec(ctx, "DELETE FROM nodes WHERE id=ANY($1)", []int64{target, sibling}) })
	path := fmt.Sprintf("admin/nodes/%d", target)
	securityStatus(t, f.request(f.user, path, "DELETE", nil), http.StatusForbidden)
	securityStatus(t, f.request(f.admin, path, "DELETE", nil), http.StatusAccepted)
	securityStatus(t, f.request(f.admin, path, "DELETE", nil), http.StatusAccepted)
	securityStatus(t, f.request(f.admin, path, "PUT", NodeInput{}), http.StatusConflict)

	stream := &StreamServer{Server: f.s}
	poll := func(node int64, instance, token string, revision int64) string {
		t.Helper()
		message, err := stream.exchangeWithCredential(ctx, f.p, node, &pb.AgentMessage{
			InstanceId: instance, ProtocolVersion: pb.CurrentVersion, UninstallSupported: true, AppliedRevision: revision,
		}, Hash(token))
		if err != nil {
			t.Fatal(err)
		}
		return message.GetUninstallTicket()
	}
	if ticket := poll(sibling, siblingInstance, siblingToken, 10); ticket != "" {
		t.Fatal("uninstall leaked to a sibling Agent")
	}
	if ticket := poll(target, targetInstance, targetToken, 1); ticket != "" {
		t.Fatal("uninstall sent before disabled configuration was applied")
	}
	ticket := poll(target, targetInstance, targetToken, 10)
	if !hashOK(ticket) {
		t.Fatal("disabled target was not sent its pending task")
	}
	report := func(token, ticket, detail string, checkOnly bool) codes.Code {
		reportCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
		_, err := stream.ReportUninstall(reportCtx, &pb.UninstallReport{Ticket: ticket, Error: detail, CheckOnly: checkOnly})
		return status.Code(err)
	}
	if got := report(siblingToken, ticket, "", false); got != codes.PermissionDenied {
		t.Fatalf("sibling Agent could complete target task: %s", got)
	}
	if got := report(targetToken, ticket, "", true); got != codes.OK {
		t.Fatalf("target preflight failed: %s", got)
	}
	if _, err := f.s.nodeForToken(ctx, targetToken); err != nil {
		t.Fatalf("preflight removed the node before host confirmation: %v", err)
	}
	if got := report(targetToken, ticket, "local helper failed", false); got != codes.OK {
		t.Fatalf("target failure report: %s", got)
	}
	if next := poll(target, targetInstance, targetToken, 10); next != "" {
		t.Fatal("failed task was automatically redelivered")
	}
	securityStatus(t, f.request(f.admin, path, "DELETE", nil), http.StatusAccepted)
	next := poll(target, targetInstance, targetToken, 10)
	if !hashOK(next) || next == ticket {
		t.Fatal("retry did not rotate the work item")
	}
	if got := report(targetToken, ticket, "", true); got != codes.PermissionDenied {
		t.Fatalf("stale ticket remained authorized: %s", got)
	}
	if got := report(targetToken, next, "", false); got != codes.OK {
		t.Fatalf("target uninstall confirmation: %s", got)
	}
	if _, err := f.s.nodeForToken(ctx, targetToken); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("removed node credential still worked: %v", err)
	}
	if _, err := f.s.nodeForToken(ctx, siblingToken); err != nil {
		t.Fatalf("sibling credential affected by target uninstall: %v", err)
	}
}

func TestLegacyNodeRequiresExplicitRecordOnly(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	var id int64
	if err := f.p.QueryRow(ctx, "INSERT INTO nodes(name,token_hash,instance_id,enabled) VALUES($1,$2,$3,false) RETURNING id", Secret(), Hash(Secret()), Secret()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.p.Exec(ctx, "DELETE FROM nodes WHERE id=$1", id) })
	path := fmt.Sprintf("admin/nodes/%d", id)
	response := f.request(f.admin, path, "DELETE", nil)
	securityStatus(t, response, http.StatusConflict)
	if !strings.Contains(response.Body.String(), "node_uninstall_manual_required") {
		t.Fatal("legacy Agent deletion did not ask for a record-only confirmation")
	}
	securityStatus(t, f.request(f.admin, path+"?record_only=yes", "DELETE", nil), http.StatusBadRequest)
	securityStatus(t, f.request(f.admin, path+"?record_only=true", "DELETE", nil), http.StatusOK)
}
