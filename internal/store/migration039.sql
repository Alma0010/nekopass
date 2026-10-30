ALTER TABLE rules ADD COLUMN tls_inbound_port integer CHECK(tls_inbound_port BETWEEN 1 AND 65535);
ALTER TABLE rules DROP CONSTRAINT rules_group_tls;
ALTER TABLE rules ADD CONSTRAINT rules_group_tls CHECK((tls_inbound_port IS NULL OR ingress_group_id IS NOT NULL) AND NOT(tls_inbound_id IS NOT NULL AND ingress_group_id IS NOT NULL));
ALTER TABLE rules DROP CONSTRAINT rules_listen_port_check;
ALTER TABLE rules ADD CONSTRAINT rules_listen_port_check CHECK(
 (tls_inbound_id IS NULL AND tls_inbound_port IS NULL AND listen_port BETWEEN 1024 AND 65535)
 OR ((tls_inbound_id IS NOT NULL OR tls_inbound_port IS NOT NULL) AND listen_port=0 AND protocol='tcp' AND jsonb_array_length(sni_names) BETWEEN 1 AND 32));
CREATE VIEW rule_tls_inbounds AS
 SELECT r.id AS rule_id,ri.node_id,t.id AS tls_inbound_id,t.port
 FROM rules r JOIN rule_ingress_nodes ri ON ri.rule_id=r.id JOIN node_tls_inbounds t ON t.node_id=ri.node_id
 AND ((r.ingress_group_id IS NULL AND t.id=r.tls_inbound_id) OR (r.ingress_group_id IS NOT NULL AND t.port=r.tls_inbound_port));
CREATE OR REPLACE FUNCTION reserve_rule_ports() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM rule_ports WHERE rule_id=NEW.id;
 IF NEW.tls_inbound_id IS NULL AND NEW.tls_inbound_port IS NULL THEN
 INSERT INTO rule_ports(node_id,protocol,listen_port,rule_id) SELECT node_id,NEW.protocol,NEW.listen_port,NEW.id FROM rule_ingress_nodes WHERE rule_id=NEW.id;
 END IF;
 INSERT INTO rule_usage(node_id,rule_id,user_id) SELECT node_id,NEW.id,NEW.user_id FROM rule_ingress_nodes WHERE rule_id=NEW.id ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
DROP TRIGGER reserve_rule_ports ON rules;
CREATE TRIGGER reserve_rule_ports AFTER INSERT OR UPDATE OF node_id,ingress_group_id,listen_port,protocol,tls_inbound_id,tls_inbound_port ON rules FOR EACH ROW EXECUTE FUNCTION reserve_rule_ports();
CREATE OR REPLACE FUNCTION reserve_rule_sni() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM rule_sni_names WHERE rule_id=NEW.id;
 INSERT INTO rule_sni_names(tls_inbound_id,name,rule_id) SELECT t.tls_inbound_id,s.value,NEW.id FROM rule_tls_inbounds t CROSS JOIN jsonb_array_elements_text(NEW.sni_names) s WHERE t.rule_id=NEW.id;
 RETURN NEW;
END $$;
DROP TRIGGER reserve_rule_sni ON rules;
CREATE TRIGGER reserve_rule_sni AFTER INSERT OR UPDATE OF tls_inbound_id,tls_inbound_port,sni_names,ingress_group_id,config_revision ON rules FOR EACH ROW EXECUTE FUNCTION reserve_rule_sni();
UPDATE revision SET value=value+1;
INSERT INTO schema_version VALUES(39);
