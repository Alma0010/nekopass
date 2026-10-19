-- Group bindings remain one rule and resolve current members on each sync.
ALTER TABLE rules ALTER COLUMN node_id DROP NOT NULL;
ALTER TABLE rules ADD COLUMN ingress_group_id bigint REFERENCES node_groups(id);
ALTER TABLE rules ADD CONSTRAINT rules_ingress_binding CHECK(num_nonnulls(node_id,ingress_group_id)=1);
ALTER TABLE rules ADD CONSTRAINT rules_group_tls CHECK(ingress_group_id IS NULL OR tls_inbound_id IS NULL);
CREATE INDEX rules_ingress_group ON rules(ingress_group_id) WHERE ingress_group_id IS NOT NULL;
-- Disabled members retain their ports; dispatch checks group authorization separately.
CREATE VIEW rule_ingress_nodes AS
 SELECT id AS rule_id,node_id FROM rules WHERE node_id IS NOT NULL
 UNION ALL
 SELECT r.id,m.node_id FROM rules r JOIN node_group_members m ON m.group_id=r.ingress_group_id
 JOIN nodes n ON n.id=m.node_id AND n.ingress_enabled;
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
DROP TRIGGER reserve_rule_ports ON rules;
CREATE TRIGGER reserve_rule_ports AFTER INSERT OR UPDATE OF node_id,ingress_group_id,listen_port,protocol,tls_inbound_id ON rules
 FOR EACH ROW EXECUTE FUNCTION reserve_rule_ports();
CREATE FUNCTION refresh_group_rule_ports() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER refresh_group_rule_ports AFTER INSERT OR DELETE ON node_group_members
 FOR EACH ROW EXECUTE FUNCTION refresh_group_rule_ports();
CREATE TRIGGER refresh_node_group_rule_ports AFTER UPDATE OF ingress_enabled ON nodes
 FOR EACH ROW WHEN (OLD.ingress_enabled IS DISTINCT FROM NEW.ingress_enabled) EXECUTE FUNCTION refresh_group_rule_ports();
UPDATE revision SET value=value+1 WHERE id=1;
INSERT INTO schema_version VALUES(29);
