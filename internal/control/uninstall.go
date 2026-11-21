package control

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	pb "github.com/nekopass/nekopass/internal/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var errNodeUpdateInProgress = errors.New("节点正在更新，请等待更新完成后再卸载")

func nodeHasRuleReferences(ctx context.Context, tx pgx.Tx, id int64) (bool, error) {
	var referenced bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM node_rule_references WHERE node_id=$1)", id).Scan(&referenced)
	return referenced, err
}

func validateUninstallRuleReferences(ctx context.Context, tx pgx.Tx) error {
	var referenced bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM node_uninstalls u JOIN node_rule_references r USING(node_id))").Scan(&referenced)
	if err == nil && referenced {
		return errors.New("转发规则不能关联正在卸载的节点")
	}
	return err
}

func queueNodeUninstall(ctx context.Context, tx pgx.Tx, id, actor int64, instance string) error {
	var updating bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM node_updates WHERE node_id=$1 AND state IN ('queued','running') AND requested_at>=now()-interval '15 minutes')", id).Scan(&updating); err != nil {
		return err
	}
	if updating {
		return errNodeUpdateInProgress
	}
	// A timed-out update must not be delivered with the uninstall task.
	if _, err := tx.Exec(ctx, "UPDATE node_updates SET state='failed',error='节点已请求卸载',updated_at=now() WHERE node_id=$1 AND state IN ('queued','running')", id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM node_install_tickets WHERE node_id=$1 AND used_at IS NULL", id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO node_uninstalls(node_id,ticket,instance_id,requested_by)
 VALUES($1,$2,$3,$4) ON CONFLICT(node_id) DO UPDATE
 SET ticket=EXCLUDED.ticket,instance_id=EXCLUDED.instance_id,requested_by=EXCLUDED.requested_by,state='queued',error='',requested_at=now()
 WHERE node_uninstalls.state='failed'`, id, Secret(), instance, actor)
	return err
}

func uninstallControl(ctx context.Context, tx pgx.Tx, id int64, report *pb.AgentMessage, out *pb.ControlMessage) error {
	if !report.UninstallSupported || out.Node == nil || out.Node.Enabled || report.RestoreState {
		return nil
	}
	err := tx.QueryRow(ctx, `SELECT u.ticket FROM node_uninstalls u JOIN nodes n ON n.id=u.node_id
 WHERE u.node_id=$1 AND u.state='queued' AND u.instance_id=$2 AND NOT n.enabled AND n.applied_revision>=n.config_revision`, id, report.InstanceId).Scan(&out.UninstallTicket)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func (s *StreamServer) ReportUninstall(ctx context.Context, r *pb.UninstallReport) (*pb.UninstallResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	tokens := md.Get("authorization")
	if len(tokens) != 1 || !strings.HasPrefix(tokens[0], "Bearer ") {
		return nil, status.Error(codes.Unauthenticated, "node token required")
	}
	if !hashOK(r.Ticket) || len(r.Error) > 500 || (r.CheckOnly && r.Error != "") {
		return nil, status.Error(codes.InvalidArgument, "invalid uninstall result")
	}
	tx, err := s.Server.ruleTx(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "cannot check uninstall task")
	}
	defer tx.Rollback(ctx)
	var node int64
	var actor *int64
	var enabled, sameInstance, hasLedger bool
	err = tx.QueryRow(ctx, `SELECT n.id,u.requested_by,n.enabled,n.instance_id=u.instance_id,
 EXISTS(SELECT 1 FROM grants WHERE node_id=n.id) OR EXISTS(SELECT 1 FROM rule_usage WHERE node_id=n.id)
 FROM node_uninstalls u JOIN nodes n ON n.id=u.node_id
 WHERE u.ticket=$1 AND n.token_hash=$2 AND u.state='queued' FOR UPDATE OF n`, r.Ticket, Hash(strings.TrimPrefix(tokens[0], "Bearer "))).Scan(&node, &actor, &enabled, &sameInstance, &hasLedger)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !sameInstance {
		return nil, status.Error(codes.PermissionDenied, "uninstall task is no longer authorized for this node")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "cannot check uninstall task")
	}
	referenced, err := nodeHasRuleReferences(ctx, tx, node)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "cannot check node rules")
	}
	if enabled || referenced {
		return nil, status.Error(codes.FailedPrecondition, "disable the node and detach its forwarding rules")
	}
	response := &pb.UninstallResponse{}
	if r.CheckOnly {
		return response, nil
	}
	if r.Error != "" {
		if _, err = tx.Exec(ctx, "UPDATE node_uninstalls SET state='failed',error=$2 WHERE node_id=$1", node, r.Error); err == nil {
			err = tx.Commit(ctx)
		}
	} else {
		err = completeNodeUninstall(ctx, tx, node, actor, hasLedger)
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "cannot save uninstall result")
	}
	return response, nil
}

func completeNodeUninstall(ctx context.Context, tx pgx.Tx, node int64, actor *int64, hasLedger bool) error {
	if hasLedger {
		if err := archiveNodeAccounting(ctx, tx, node, actor); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM nodes WHERE id=$1", node); err != nil {
		return err
	}
	return finishRules(ctx, tx)
}
