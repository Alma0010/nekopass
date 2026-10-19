package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Resolve the binding, not the union of other groups that authorize its members.
func ruleIngressIDs(ctx context.Context, tx pgx.Tx, in RuleInput) ([]int64, error) {
	if in.IngressGroupID == 0 {
		return []int64{in.NodeID}, nil
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_node_groups ug JOIN node_groups g ON g.id=ug.group_id AND g.enabled WHERE ug.user_id=$1 AND g.id=$2)`, in.UserID, in.IngressGroupID).Scan(&allowed); err != nil {
		return nil, err
	}
	if !allowed {
		return nil, errors.New("用户没有此入口节点组的权限，或节点组已停用")
	}
	rows, err := tx.Query(ctx, `SELECT n.id FROM node_group_members m JOIN nodes n ON n.id=m.node_id WHERE m.group_id=$1 AND n.ingress_enabled ORDER BY n.id`, in.IngressGroupID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err == nil && len(ids) == 0 {
		return nil, errors.New("此节点组没有入口节点")
	}
	return ids, err
}

func validateRuleIngress(ctx context.Context, tx pgx.Tx, in RuleInput, ruleID, node int64) error {
	var enabled, ingress, direct bool
	if err := tx.QueryRow(ctx, `SELECT enabled,ingress_enabled,allow_direct FROM nodes WHERE id=$1`, node).Scan(&enabled, &ingress, &direct); err != nil {
		return err
	}
	// Disabled group members retain reservations and rejoin when enabled.
	if in.IngressGroupID == 0 || enabled {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_nodes WHERE user_id=$1 AND node_id=$2)`, in.UserID, node).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errors.New("用户没有此入口节点的权限")
		}
	}
	if !ingress {
		return errors.New("此节点未启用入口功能")
	}
	if in.EgressNodeID == 0 {
		if !direct {
			return fmt.Errorf("入口节点 #%d 不允许直转，请选择所有入口都可用的出口", node)
		}
	} else {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_tunnel_links l JOIN nodes n ON n.id=l.egress_node_id AND n.enabled AND n.tunnel_exit_enabled JOIN user_nodes u ON u.node_id=n.id AND u.user_id=$3 WHERE l.ingress_node_id=$1 AND l.egress_node_id=$2 AND l.ingress_node_id<>l.egress_node_id)`, node, in.EgressNodeID, in.UserID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errors.New("出口节点未与所有入口关联、不可用或用户无权使用")
		}
	}
	copy := in
	copy.NodeID = node
	return validateTLSRule(ctx, tx, &copy, ruleID)
}

func allocateRulePort(ctx context.Context, tx pgx.Tx, in *RuleInput, id int64, nodes []int64) error {
	var lo, hi int
	if err := tx.QueryRow(ctx, `SELECT max(port_min),min(port_max) FROM nodes WHERE id=ANY($1)`, nodes).Scan(&lo, &hi); err != nil {
		return err
	}
	if lo > hi {
		return errors.New("组内入口节点没有共同的监听端口范围")
	}
	if in.ListenPort != 0 && (in.ListenPort < lo || in.ListenPort > hi) {
		return fmt.Errorf("监听端口须在所有入口的共同范围 %d–%d 内", lo, hi)
	}
	if in.ListenPort == 0 {
		if err := tx.QueryRow(ctx, `SELECT p FROM generate_series($1::integer,$2::integer) p WHERE NOT EXISTS(SELECT 1 FROM rule_ports WHERE node_id=ANY($3) AND listen_port=p AND COALESCE(rule_id,0)<>$4 AND ($5='tcp_udp' OR protocol=$5)) AND NOT EXISTS(SELECT 1 FROM nodes WHERE id=ANY($3) AND tunnel_exit_enabled AND tunnel_listen_port=p AND ($5='tcp_udp' OR CASE WHEN tunnel_protocol IN ('plain_udp','dtls_udp') THEN 'udp' ELSE 'tcp' END=$5)) ORDER BY random() LIMIT 1`, lo, hi, nodes, id, in.Protocol).Scan(&in.ListenPort); err != nil {
			return errors.New("所有入口之间没有可共同分配的端口")
		}
	}
	var conflict bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rule_ports WHERE node_id=ANY($1) AND listen_port=$2 AND COALESCE(rule_id,0)<>$3 AND ($4='tcp_udp' OR protocol=$4)) OR EXISTS(SELECT 1 FROM nodes WHERE id=ANY($1) AND tunnel_exit_enabled AND tunnel_listen_port=$2 AND (($4='tcp_udp') OR CASE WHEN tunnel_protocol IN ('plain_udp','dtls_udp') THEN 'udp' ELSE 'tcp' END=$4))`, nodes, in.ListenPort, id, in.Protocol).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return errors.New("监听端口已被组内某个入口占用（暂停规则仍保留端口）")
	}
	return nil
}

// Recheck after node/group edits, once links and membership have both been saved.
func validateGroupRuleTopology(ctx context.Context, tx pgx.Tx) error {
	var invalid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rules r JOIN rule_ingress_nodes ri ON ri.rule_id=r.id JOIN nodes n ON n.id=ri.node_id LEFT JOIN nodes en ON en.id=r.egress_node_id WHERE r.ingress_group_id IS NOT NULL AND (r.listen_port NOT BETWEEN n.port_min AND n.port_max OR (r.protocol='tcp' AND n.tls_inbound_only) OR (r.egress_node_id IS NULL AND NOT n.allow_direct) OR (r.egress_node_id IS NOT NULL AND (n.id=en.id OR NOT en.tunnel_exit_enabled OR NOT EXISTS(SELECT 1 FROM node_tunnel_links l WHERE l.ingress_node_id=n.id AND l.egress_node_id=en.id) OR (r.protocol<>'udp' AND en.tunnel_protocol IN ('plain_udp','dtls_udp')))) OR (n.tunnel_exit_enabled AND n.tunnel_listen_port=r.listen_port AND CASE WHEN n.tunnel_protocol IN ('plain_udp','dtls_udp') THEN 'udp' ELSE 'tcp' END=r.protocol)))`).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("节点组已有规则与入口端口范围、直转权限、TLS 限制或出口关联冲突，请先调整规则")
	}
	return nil
}

func (s *Server) ruleNodeGroups(w http.ResponseWriter, r *http.Request) {
	u := ruleActor(r)
	rows, err := s.Pool.Query(r.Context(), `SELECT g.id,g.name,g.description,g.enabled,g.sort_order,g.strategy,COALESCE((SELECT array_agg(n.id ORDER BY n.id) FROM node_group_members m JOIN nodes n ON n.id=m.node_id AND n.ingress_enabled WHERE m.group_id=g.id),'{}') AS node_ids,COALESCE((SELECT jsonb_agg(jsonb_build_object('id',n.id,'name',n.name,'allow_direct',n.allow_direct,'port_min',n.port_min,'port_max',n.port_max,'enabled',n.enabled,'tls_inbound_only',n.tls_inbound_only) ORDER BY n.id) FROM node_group_members m JOIN nodes n ON n.id=m.node_id AND n.ingress_enabled WHERE m.group_id=g.id),'[]') AS members FROM node_groups g JOIN user_node_groups ug ON ug.group_id=g.id WHERE ug.user_id=$1 AND g.enabled ORDER BY g.sort_order,g.id`, u.ID)
	s.sendRows(w, rows, err)
}
