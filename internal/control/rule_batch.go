package control

import "errors"

// Only explicitly supplied fields are changed; false and egress zero are values.
// Ownership, quotas, targets, SNI and listener settings cannot be patched here.
type RuleBatchPatch struct {
	NodeID             *int64  `json:"node_id,omitempty"`
	EgressGroupID      *int64  `json:"egress_group_id,omitempty"`
	EgressNodeID       *int64  `json:"egress_node_id,omitempty"`
	Balance            *string `json:"balance,omitempty"`
	ProxyAcceptEnabled *bool   `json:"proxy_accept_enabled,omitempty"`
	ProxySendEnabled   *bool   `json:"proxy_send_enabled,omitempty"`
}

func (p *RuleBatchPatch) validate() error {
	if p == nil || p.NodeID == nil && p.EgressNodeID == nil && p.EgressGroupID == nil && p.Balance == nil && p.ProxyAcceptEnabled == nil && p.ProxySendEnabled == nil {
		return errors.New("请选择至少一个需要更新的字段")
	}
	if p.NodeID != nil && *p.NodeID <= 0 || p.EgressNodeID != nil && *p.EgressNodeID < 0 || p.EgressGroupID != nil && *p.EgressGroupID < 0 || p.EgressGroupID != nil && p.EgressNodeID != nil && *p.EgressGroupID != 0 && *p.EgressNodeID != 0 {
		return errors.New("入口或出口节点无效")
	}
	if p.Balance != nil && *p.Balance != "random" && *p.Balance != "round_robin" && *p.Balance != "least_connections" {
		return errors.New("负载均衡策略无效")
	}
	return nil
}

func (p *RuleBatchPatch) apply(in *RuleInput) error {
	if p.NodeID != nil && *p.NodeID != in.NodeID {
		if in.TLSInboundID != 0 {
			return errors.New("共享 TLS 规则切换入口时，请逐条编辑并选择新节点的共享 TLS 入口")
		}
		in.NodeID = *p.NodeID
		in.ListenPort = 0 // Follow the existing batch ingress-switch allocation policy.
	}
	if p.EgressNodeID != nil {
		in.EgressNodeID = *p.EgressNodeID
		in.EgressGroupID = 0
	}
	if p.EgressGroupID != nil {
		in.EgressGroupID = *p.EgressGroupID
		if in.EgressGroupID != 0 {
			in.EgressNodeID = 0
		}
	}
	if p.Balance != nil {
		in.Balance = *p.Balance
	}
	if p.ProxyAcceptEnabled != nil {
		if in.TLSInboundID != 0 {
			return errors.New("共享 TLS 入站的 Proxy Protocol 接收由管理员配置")
		}
		if !*p.ProxyAcceptEnabled {
			in.ProxyAccept = "off"
		} else if in.ProxyAccept == "" || in.ProxyAccept == "off" {
			in.ProxyAccept = "auto"
		}
	}
	if p.ProxySendEnabled != nil {
		if !*p.ProxySendEnabled {
			in.ProxySend = "off"
		} else if in.ProxySend == "" || in.ProxySend == "off" {
			in.ProxySend = "v2"
		}
	}
	return nil
}
