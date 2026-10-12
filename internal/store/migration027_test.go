package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSNIPassthroughMigrationPreservesRulesAndUsage(t *testing.T) {
	dsn := os.Getenv("NEKOPASS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a dedicated PostgreSQL test database")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(config.Database, "_test") {
		t.Fatal("refusing migration test outside a dedicated test database")
	}
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// Apply the actual old schema in an isolated, automatically rolled back schema.
	name := fmt.Sprintf("sni_migration_%d", time.Now().UnixNano())
	exec := func(sql string) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + name)
	exec("SET LOCAL search_path TO " + name)
	for _, sql := range []string{schema, migration002, migration003, migration004, migration005, migration006, migration007, migration008, migration009, migration010, migration011, migration012, migration013, migration014, migration015, migration016, migration017, migration018, migration019, migration020, migration021, migration022, migration023, migration024, migration025, migration026} {
		exec(sql)
	}
	exec(`
INSERT INTO users(id,username,password_hash,traffic_base_bytes,quota_bytes) VALUES(1,'migration@example.test','fixture',321,1000000);
UPDATE users SET quota_bytes=1000000 WHERE id=1;
INSERT INTO nodes(id,name,token_hash,tls_certificate,tls_private_key) VALUES(1,'migration','fixture','exit-cert','exit-key');
INSERT INTO node_tls_inbounds(id,node_id,port,config,certificate,private_key,certificate_status)
 VALUES(1,1,443,'{"name":"shared","enabled":true,"port":443,"tls":{"server_name":"app.example.test","dns_credentials":{"fixture":"credential"}},"min_tls_version":"1.2"}','entry-cert','entry-key','ready');
INSERT INTO rules(id,user_id,node_id,listen_port,target_host,target_port,tls_inbound_id,sni_names,tls_alpn,origin_tls,enabled)
 VALUES(1,1,1,0,'target.example.test',443,1,'["app.example.test"]','["http/1.1"]','{"enabled":true}',false);
INSERT INTO rule_usage(node_id,rule_id,user_id,traffic) VALUES(1,1,1,123);
INSERT INTO grants(node_id,user_id,issued,spent,traffic) VALUES(1,1,1000,500,123);
INSERT INTO wallet_accounts(user_id,balance_cents) VALUES(1,4567);
INSERT INTO inbound_acme_accounts(inbound_id,directory_url,email,private_key) VALUES(1,'https://acme.example.test','fixture@example.test','entry-account');
INSERT INTO node_acme_accounts(node_id,directory_url,email,private_key) VALUES(1,'https://acme.example.test','fixture@example.test','exit-account');
INSERT INTO node_acme_challenges(node_id,domain,token,key_authorization,revision,expires_at,inbound_id)
 VALUES(1,'app.example.test','entry','fixture',1,now()+interval '5 minutes',1),(1,'exit.example.test','exit','fixture',1,now()+interval '5 minutes',NULL);
`)
	exec(migration027)
	var correct bool
	err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*)=1 FROM rules WHERE id=1 AND tls_inbound_id=1 AND listen_port=0 AND sni_names='["app.example.test"]' AND NOT enabled AND origin_tls='{}' AND tls_alpn='[]')
 AND (SELECT count(*)=1 FROM node_tls_inbounds WHERE id=1 AND port=443 AND NOT(config?'tls') AND NOT(config?'min_tls_version') AND certificate='' AND private_key='')
 AND (SELECT count(*)=1 FROM rule_ports WHERE tls_inbound_id=1 AND listen_port=443)
 AND (SELECT count(*)=1 FROM rule_sni_names WHERE rule_id=1 AND name='app.example.test')
 AND (SELECT traffic=123 FROM rule_usage WHERE rule_id=1)
 AND (SELECT issued=1000 AND spent=500 AND traffic=123 FROM grants WHERE node_id=1 AND user_id=1)
 AND (SELECT traffic_base_bytes=321 AND quota_bytes=1000000 FROM users WHERE id=1)
 AND (SELECT balance_cents=4567 FROM wallet_accounts WHERE user_id=1)
 AND (SELECT tls_certificate='exit-cert' AND tls_private_key='exit-key' FROM nodes WHERE id=1)
 AND (SELECT count(*)=1 FROM node_acme_accounts WHERE private_key='exit-account')
 AND (SELECT count(*)=0 FROM inbound_acme_accounts)
 AND (SELECT count(*)=1 FROM node_acme_challenges WHERE inbound_id IS NULL AND token='exit')
 AND (SELECT count(*)=0 FROM node_acme_challenges WHERE inbound_id IS NOT NULL)
 AND (SELECT max(version)=27 FROM schema_version)`).Scan(&correct)
	if err != nil || !correct {
		t.Fatal("migration lost rules, reservations, usage, balance or exit certificate state", err)
	}
}
