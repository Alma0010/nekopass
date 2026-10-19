ALTER TABLE node_groups DROP CONSTRAINT node_groups_strategy_check;
UPDATE node_groups SET strategy='least_connections' WHERE strategy='manual';
ALTER TABLE node_groups ALTER COLUMN strategy SET DEFAULT 'least_connections';
ALTER TABLE node_groups ADD CHECK(strategy IN ('least_connections','round_robin','random'));
ALTER TABLE node_groups ADD COLUMN health_interval_seconds integer NOT NULL DEFAULT 5 CHECK(health_interval_seconds BETWEEN 2 AND 60);
ALTER TABLE node_groups ADD COLUMN health_failures integer NOT NULL DEFAULT 3 CHECK(health_failures BETWEEN 1 AND 20);
ALTER TABLE node_groups ADD COLUMN health_successes integer NOT NULL DEFAULT 2 CHECK(health_successes BETWEEN 1 AND 20);
ALTER TABLE node_group_members ADD COLUMN role text NOT NULL DEFAULT 'active' CHECK(role IN ('active','standby','disabled'));
ALTER TABLE node_group_members ADD CHECK(weight<=1000);
ALTER TABLE rules ADD COLUMN egress_group_id bigint REFERENCES node_groups(id);
ALTER TABLE rules ADD CHECK(egress_node_id IS NULL OR egress_group_id IS NULL);
CREATE INDEX rules_egress_group ON rules(egress_group_id) WHERE egress_group_id IS NOT NULL;
-- The same authorization projection is used for control distribution and group validation.
CREATE VIEW rule_outbound_routes AS
 SELECT r.id AS rule_id,n.id AS egress_node_id,1 AS weight,'active'::text AS role
 FROM rules r JOIN nodes n ON n.id=r.egress_node_id AND n.enabled AND n.tunnel_exit_enabled
 JOIN node_tunnel_links l ON l.ingress_node_id=r.node_id AND l.egress_node_id=n.id
 JOIN user_nodes a ON a.user_id=r.user_id AND a.node_id=n.id
 WHERE n.id<>r.node_id AND (r.protocol='udp' OR n.tunnel_protocol NOT IN ('plain_udp','dtls_udp'))
 UNION ALL
 SELECT r.id,n.id,m.weight,m.role
 FROM rules r JOIN user_node_groups ug ON ug.user_id=r.user_id AND ug.group_id=r.egress_group_id
 JOIN node_groups g ON g.id=ug.group_id AND g.enabled
 JOIN node_group_members m ON m.group_id=g.id AND m.role<>'disabled'
 JOIN nodes n ON n.id=m.node_id AND n.enabled AND n.tunnel_exit_enabled AND n.id<>r.node_id
 JOIN node_tunnel_links l ON l.ingress_node_id=r.node_id AND l.egress_node_id=n.id
 JOIN user_nodes a ON a.user_id=r.user_id AND a.node_id=n.id
 WHERE r.protocol='udp' OR n.tunnel_protocol NOT IN ('plain_udp','dtls_udp');
UPDATE revision SET value=value+1;
INSERT INTO schema_version VALUES(29);
