ALTER TABLE nodes ADD COLUMN uninstall_supported boolean NOT NULL DEFAULT false;
CREATE TABLE node_uninstalls (
 node_id bigint PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
 ticket text NOT NULL UNIQUE,
 instance_id text NOT NULL,
 requested_by bigint REFERENCES users(id) ON DELETE SET NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','failed')),
 error text NOT NULL DEFAULT '',
 requested_at timestamptz NOT NULL DEFAULT now()
);
CREATE VIEW node_rule_references AS
 SELECT node_id FROM rule_ingress_nodes
 UNION ALL
 SELECT egress_node_id FROM rules WHERE egress_node_id IS NOT NULL
 UNION ALL
 SELECT m.node_id FROM rules r JOIN node_group_members m ON m.group_id=r.egress_group_id
 JOIN nodes n ON n.id=m.node_id AND n.tunnel_exit_enabled WHERE m.role<>'disabled';
INSERT INTO schema_version VALUES(43);
