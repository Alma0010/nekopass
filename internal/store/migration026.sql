CREATE TABLE node_tls_inbounds (
 id bigserial PRIMARY KEY,node_id bigint NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 config jsonb NOT NULL,port integer NOT NULL CHECK(port BETWEEN 1 AND 65535),
 certificate text NOT NULL DEFAULT '',private_key text NOT NULL DEFAULT '',
 certificate_status text NOT NULL DEFAULT 'unconfigured',certificate_error text NOT NULL DEFAULT '',
 not_after timestamptz,generation bigint NOT NULL DEFAULT 1,
 UNIQUE(node_id,id),UNIQUE(node_id,port)
);
ALTER TABLE nodes ADD COLUMN tls_inbound_only boolean NOT NULL DEFAULT false;
ALTER TABLE rules ADD COLUMN tls_inbound_id bigint;
ALTER TABLE rules ADD COLUMN sni_names jsonb NOT NULL DEFAULT '[]';
ALTER TABLE rules ADD COLUMN tls_alpn jsonb NOT NULL DEFAULT '[]';
ALTER TABLE rules ADD COLUMN origin_tls jsonb NOT NULL DEFAULT '{}';
ALTER TABLE rules ADD FOREIGN KEY(node_id,tls_inbound_id) REFERENCES node_tls_inbounds(node_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE rules DROP CONSTRAINT rules_listen_port_check;
ALTER TABLE rules ADD CONSTRAINT rules_listen_port_check CHECK(
 (tls_inbound_id IS NULL AND listen_port BETWEEN 1024 AND 65535)
 OR (tls_inbound_id IS NOT NULL AND listen_port=0 AND protocol='tcp' AND jsonb_array_length(sni_names) BETWEEN 1 AND 32));
ALTER TABLE rule_ports ALTER COLUMN rule_id DROP NOT NULL;
ALTER TABLE rule_ports ADD COLUMN tls_inbound_id bigint REFERENCES node_tls_inbounds(id) ON DELETE CASCADE;
ALTER TABLE rule_ports ADD CHECK(num_nonnulls(rule_id,tls_inbound_id)=1);
CREATE OR REPLACE FUNCTION reserve_rule_ports() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM rule_ports WHERE rule_id=NEW.id;
 IF NEW.tls_inbound_id IS NULL THEN
  INSERT INTO rule_ports(node_id,protocol,listen_port,rule_id) VALUES(NEW.node_id,NEW.protocol,NEW.listen_port,NEW.id);
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER reserve_rule_ports ON rules;
CREATE TRIGGER reserve_rule_ports AFTER INSERT OR UPDATE OF node_id,listen_port,protocol,tls_inbound_id ON rules FOR EACH ROW EXECUTE FUNCTION reserve_rule_ports();
CREATE FUNCTION reserve_tls_inbound_port() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM rule_ports WHERE tls_inbound_id=NEW.id;
 INSERT INTO rule_ports(node_id,protocol,listen_port,tls_inbound_id) VALUES(NEW.node_id,'tcp',NEW.port,NEW.id);
 RETURN NEW;
END $$;
CREATE TRIGGER reserve_tls_inbound_port AFTER INSERT OR UPDATE OF node_id,port ON node_tls_inbounds FOR EACH ROW EXECUTE FUNCTION reserve_tls_inbound_port();
CREATE TABLE rule_sni_names (
 tls_inbound_id bigint NOT NULL REFERENCES node_tls_inbounds(id) ON DELETE CASCADE,
 name text NOT NULL,rule_id bigint NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
 PRIMARY KEY(tls_inbound_id,name)
);
CREATE FUNCTION reserve_rule_sni() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM rule_sni_names WHERE rule_id=NEW.id;
 IF NEW.tls_inbound_id IS NOT NULL THEN
  INSERT INTO rule_sni_names(tls_inbound_id,name,rule_id) SELECT NEW.tls_inbound_id,value,NEW.id FROM jsonb_array_elements_text(NEW.sni_names);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER reserve_rule_sni AFTER INSERT OR UPDATE OF tls_inbound_id,sni_names ON rules FOR EACH ROW EXECUTE FUNCTION reserve_rule_sni();
CREATE TABLE inbound_acme_accounts (
 inbound_id bigint PRIMARY KEY REFERENCES node_tls_inbounds(id) ON DELETE CASCADE,
 directory_url text NOT NULL,email text NOT NULL,private_key text NOT NULL,registration jsonb NOT NULL DEFAULT '{}'
);
ALTER TABLE node_acme_challenges ADD COLUMN inbound_id bigint REFERENCES node_tls_inbounds(id) ON DELETE CASCADE;
INSERT INTO schema_version VALUES(26);
