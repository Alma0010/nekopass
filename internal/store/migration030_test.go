package store

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGroupBindingMigrationPreservesSingleNodeRulesAndUsage(t *testing.T) {
	dsn := os.Getenv("NEKOPASS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a dedicated PostgreSQL test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("requires isolated *_test database")
	}
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("group_migration_%d", time.Now().UnixNano())
	exec("CREATE SCHEMA " + name)
	exec("SET LOCAL search_path TO " + name)
	for _, sql := range []string{schema, migration002, migration003, migration004, migration005, migration006, migration007, migration008, migration009, migration010, migration011, migration012, migration013, migration014, migration015, migration016, migration017, migration018, migration019, migration020, migration021, migration022, migration023, migration024, migration025, migration026, migration027, migration028, migration029} {
		exec(sql)
	}
	exec(`INSERT INTO users(id,username,password_hash) VALUES(1,'migration@example.test','fixture');
 INSERT INTO nodes(id,name,token_hash) VALUES(1,'migration','fixture');
 INSERT INTO rules(id,user_id,node_id,listen_port,target_host,target_port,name) VALUES(1,1,1,24001,'target.example.test',443,'existing');
 INSERT INTO rule_usage(node_id,rule_id,user_id,traffic) VALUES(1,1,1,123);
 INSERT INTO node_groups(id,name) VALUES(1,'fixture-group');
 INSERT INTO node_group_members(group_id,node_id) VALUES(1,1);`)
	exec("SET CONSTRAINTS ALL IMMEDIATE")
	exec(migration030)
	var node, traffic int64
	var group *int64
	var port, instances int
	if err := tx.QueryRow(ctx, `SELECT r.node_id,r.ingress_group_id,r.listen_port,u.traffic,(SELECT count(*) FROM rule_ingress_nodes WHERE rule_id=r.id) FROM rules r JOIN rule_usage u ON u.rule_id=r.id WHERE r.id=1`).Scan(&node, &group, &port, &traffic, &instances); err != nil {
		t.Fatal(err)
	}
	if node != 1 || group != nil || port != 24001 || traffic != 123 || instances != 1 {
		t.Fatal("existing rule changed", node, group, port, traffic, instances)
	}
	exec(`UPDATE rules SET node_id=NULL,ingress_group_id=1 WHERE id=1`)
	var owners int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM rule_ports WHERE rule_id=1 AND node_id=1 AND listen_port=24001").Scan(&owners); err != nil || owners != 1 {
		t.Fatal("switch lost reservation", owners, err)
	}
	if err := tx.QueryRow(ctx, "SELECT traffic FROM rule_usage WHERE rule_id=1 AND node_id=1").Scan(&traffic); err != nil || traffic != 123 {
		t.Fatal("switch lost usage", traffic, err)
	}
}
