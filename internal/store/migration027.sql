-- Shared inbound TLS is end-to-end SNI passthrough; retain ports, names and usage.
UPDATE node_tls_inbounds SET config=config-'tls'-'min_tls_version',
 certificate='',private_key='',certificate_status='unused',certificate_error='',not_after=NULL,generation=generation+1;
DELETE FROM node_acme_challenges WHERE inbound_id IS NOT NULL;
DELETE FROM inbound_acme_accounts;
UPDATE rules SET tls_alpn='[]',origin_tls='{}',config_revision=(SELECT value+1 FROM revision WHERE id=1) WHERE tls_inbound_id IS NOT NULL;
UPDATE nodes SET config_revision=(SELECT value+1 FROM revision WHERE id=1) WHERE id IN (SELECT node_id FROM node_tls_inbounds);
UPDATE revision SET value=value+1 WHERE id=1;
INSERT INTO schema_version VALUES(27);
