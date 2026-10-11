CREATE TABLE node_ip_recovery (
 node_id bigint PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
 config jsonb NOT NULL,
 generation bigint NOT NULL DEFAULT 1,
 reset_sequence bigint NOT NULL DEFAULT 0,
 status jsonb NOT NULL DEFAULT '{}'::jsonb,
 status_received_at timestamptz
);
CREATE UNIQUE INDEX node_ip_recovery_active_instance ON node_ip_recovery
 ((config->>'instance_type'),(config->>'account_id'),(config->>'region_name'),(config->>'instance_id'))
 WHERE config->>'enabled'='true';
INSERT INTO schema_version VALUES(25);
