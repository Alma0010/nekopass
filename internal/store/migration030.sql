-- Keep upstream schema 29; add this fork's ingress-group binding as schema 30.
-- An earlier fork build used version 29 for ingress groups, so keep that data.
DO $binding$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='rules' AND column_name='ingress_group_id') THEN
ALTER TABLE rules ALTER COLUMN node_id DROP NOT NULL;
ALTER TABLE rules ADD COLUMN ingress_group_id bigint REFERENCES node_groups(id);
ALTER TABLE rules ADD CONSTRAINT rules_ingress_binding CHECK(num_nonnulls(node_id,ingress_group_id)=1);
ALTER TABLE rules ADD CONSTRAINT rules_group_tls CHECK(ingress_group_id IS NULL OR tls_inbound_id IS NULL);
CREATE INDEX rules_ingress_group ON rules(ingress_group_id) WHERE ingress_group_id IS NOT NULL;
 END IF; END $binding$;
CREATE OR REPLACE VIEW rule_ingress_nodes AS
 SELECT id AS rule_id,node_id FROM rules WHERE node_id IS NOT NULL
 UNION ALL
 SELECT r.id,m.node_id FROM rules r JOIN node_group_members m ON m.group_id=r.ingress_group_id
 JOIN nodes n ON n.id=m.node_id AND n.ingress_enabled WHERE m.role<>'disabled';
CREATE OR REPLACE FUNCTION reserve_rule_ports() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM rule_ports WHERE rule_id=NEW.id;
 IF NEW.tls_inbound_id IS NULL THEN
  INSERT INTO rule_ports(node_id,protocol,listen_port,rule_id)
   SELECT node_id,NEW.protocol,NEW.listen_port,NEW.id FROM rule_ingress_nodes WHERE rule_id=NEW.id;
 END IF;
 INSERT INTO rule_usage(node_id,rule_id,user_id)
  SELECT node_id,NEW.id,NEW.user_id FROM rule_ingress_nodes WHERE rule_id=NEW.id ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS reserve_rule_ports ON rules;
CREATE TRIGGER reserve_rule_ports AFTER INSERT OR UPDATE OF node_id,ingress_group_id,listen_port,protocol,tls_inbound_id ON rules
 FOR EACH ROW EXECUTE FUNCTION reserve_rule_ports();
CREATE OR REPLACE FUNCTION refresh_group_rule_ports() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='nodes' THEN
  UPDATE rules SET ingress_group_id=ingress_group_id,config_revision=(SELECT value+1 FROM revision WHERE id=1)
   WHERE ingress_group_id IN (SELECT group_id FROM node_group_members WHERE node_id=NEW.id);
 ELSE
  UPDATE rules SET ingress_group_id=ingress_group_id,config_revision=(SELECT value+1 FROM revision WHERE id=1)
   WHERE ingress_group_id=CASE WHEN TG_OP='DELETE' THEN OLD.group_id ELSE NEW.group_id END;
 END IF;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS refresh_group_rule_ports ON node_group_members;
CREATE TRIGGER refresh_group_rule_ports AFTER INSERT OR DELETE OR UPDATE OF role ON node_group_members
 FOR EACH ROW EXECUTE FUNCTION refresh_group_rule_ports();
DROP TRIGGER IF EXISTS refresh_node_group_rule_ports ON nodes;
CREATE TRIGGER refresh_node_group_rule_ports AFTER UPDATE OF ingress_enabled ON nodes
 FOR EACH ROW WHEN (OLD.ingress_enabled IS DISTINCT FROM NEW.ingress_enabled) EXECUTE FUNCTION refresh_group_rule_ports();
UPDATE revision SET value=value+1 WHERE id=1;
INSERT INTO schema_version VALUES(30);

CREATE OR REPLACE VIEW rule_outbound_routes AS
 SELECT r.id AS rule_id,n.id AS egress_node_id,1 AS weight,'active'::text AS role,ri.node_id AS ingress_node_id
 FROM rules r JOIN rule_ingress_nodes ri ON ri.rule_id=r.id
 JOIN user_nodes ia ON ia.user_id=r.user_id AND ia.node_id=ri.node_id
 JOIN nodes n ON n.id=r.egress_node_id AND n.enabled AND n.tunnel_exit_enabled
 JOIN node_tunnel_links l ON l.ingress_node_id=ri.node_id AND l.egress_node_id=n.id
 JOIN user_nodes a ON a.user_id=r.user_id AND a.node_id=n.id
 WHERE n.id<>ri.node_id AND (r.protocol='udp' OR n.tunnel_protocol NOT IN ('plain_udp','dtls_udp')) AND (r.ingress_group_id IS NULL OR EXISTS(SELECT 1 FROM user_node_groups ug JOIN node_groups ng ON ng.id=ug.group_id AND ng.enabled WHERE ug.user_id=r.user_id AND ug.group_id=r.ingress_group_id))
 UNION ALL
 SELECT r.id,n.id,m.weight,m.role,ri.node_id
 FROM rules r JOIN rule_ingress_nodes ri ON ri.rule_id=r.id
 JOIN user_nodes ia ON ia.user_id=r.user_id AND ia.node_id=ri.node_id
 JOIN user_node_groups ug ON ug.user_id=r.user_id AND ug.group_id=r.egress_group_id
 JOIN node_groups g ON g.id=ug.group_id AND g.enabled
 JOIN node_group_members m ON m.group_id=g.id AND m.role<>'disabled'
 JOIN nodes n ON n.id=m.node_id AND n.enabled AND n.tunnel_exit_enabled AND n.id<>ri.node_id
 JOIN node_tunnel_links l ON l.ingress_node_id=ri.node_id AND l.egress_node_id=n.id
 JOIN user_nodes a ON a.user_id=r.user_id AND a.node_id=n.id
 WHERE (r.protocol='udp' OR n.tunnel_protocol NOT IN ('plain_udp','dtls_udp')) AND (r.ingress_group_id IS NULL OR EXISTS(SELECT 1 FROM user_node_groups ug JOIN node_groups ng ON ng.id=ug.group_id AND ng.enabled WHERE ug.user_id=r.user_id AND ug.group_id=r.ingress_group_id));
