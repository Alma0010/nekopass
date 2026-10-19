-- A forced node deletion removes operational configuration and credentials,
-- while retaining its accounting contributions across current quota epochs.
CREATE TABLE deleted_nodes (
 id bigint PRIMARY KEY,
 name text NOT NULL,
 deleted_at timestamptz NOT NULL DEFAULT now(),
 deleted_by bigint REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE deleted_node_grants (LIKE grants INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
ALTER TABLE deleted_node_grants ADD PRIMARY KEY(node_id,user_id,quota_epoch);
ALTER TABLE deleted_node_grants ADD FOREIGN KEY(node_id) REFERENCES deleted_nodes(id);
ALTER TABLE deleted_node_grants ADD FOREIGN KEY(user_id) REFERENCES users(id);
CREATE INDEX deleted_node_grants_user_epoch ON deleted_node_grants(user_id,quota_epoch);

CREATE TABLE deleted_node_rule_usage (LIKE rule_usage INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
ALTER TABLE deleted_node_rule_usage ADD PRIMARY KEY(node_id,rule_id);
ALTER TABLE deleted_node_rule_usage ADD FOREIGN KEY(node_id) REFERENCES deleted_nodes(id);
ALTER TABLE deleted_node_rule_usage ADD FOREIGN KEY(user_id) REFERENCES users(id);
CREATE INDEX deleted_node_rule_usage_rule ON deleted_node_rule_usage(rule_id);

CREATE OR REPLACE VIEW current_grants AS
 SELECT g.* FROM grants g JOIN users u ON u.id=g.user_id AND u.quota_epoch=g.quota_epoch
 UNION ALL
 SELECT g.* FROM deleted_node_grants g JOIN users u ON u.id=g.user_id AND u.quota_epoch=g.quota_epoch;

CREATE VIEW all_rule_usage AS
 SELECT * FROM rule_usage
 UNION ALL
 SELECT * FROM deleted_node_rule_usage;

INSERT INTO schema_version VALUES(28);
