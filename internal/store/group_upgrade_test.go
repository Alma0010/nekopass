package store

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMigrateSupportsOfficialAndForkSchema29(t *testing.T) {
	dsn := os.Getenv("NEKOPASS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires dedicated test database")
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(base.ConnConfig.Database, "_test") {
		t.Fatal("requires isolated *_test database")
	}
	ctx := context.Background()
	for _, kind := range []string{"v0185", "official29", "fork29"} {
		t.Run(kind, func(t *testing.T) {
			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(ctx)
			name := fmt.Sprintf("group_upgrade_%d", time.Now().UnixNano())
			quoted := pgx.Identifier{name}.Sanitize()
			exec := func(sql string) {
				t.Helper()
				if _, err := conn.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			exec("CREATE SCHEMA " + quoted)
			defer conn.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
			exec("SET search_path TO " + quoted)
			for _, sql := range []string{schema, migration002, migration003, migration004, migration005, migration006, migration007, migration008, migration009, migration010, migration011, migration012, migration013, migration014, migration015, migration016, migration017, migration018, migration019, migration020, migration021, migration022, migration023, migration024, migration025, migration026, migration027, migration028} {
				exec(sql)
			}
			exec(`INSERT INTO users(id,username,password_hash) VALUES(1,'migration@example.test','fixture');
   INSERT INTO nodes(id,name,token_hash) VALUES(1,'migration','fixture');
   INSERT INTO node_groups(id,name) VALUES(1,'fixture-group');
   INSERT INTO node_group_members(group_id,node_id) VALUES(1,1);
   INSERT INTO rules(id,user_id,node_id,listen_port,target_host,target_port,name) VALUES(1,1,1,24001,'target.example.test',443,'existing');
   INSERT INTO rule_usage(node_id,rule_id,user_id,traffic) VALUES(1,1,1,123);`)
			if kind == "official29" {
				exec(migration029)
			}
			if kind == "fork29" {
				data, err := os.ReadFile("testdata/legacy_ingress029.sql")
				if err != nil {
					t.Fatal(err)
				}
				exec(string(data))
				exec("UPDATE rules SET node_id=NULL,ingress_group_id=1 WHERE id=1")
			}
			cfg := base.Copy()
			cfg.ConnConfig.RuntimeParams["search_path"] = name
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			for i := 0; i < 2; i++ {
				if err := Migrate(ctx, pool); err != nil {
					t.Fatalf("migration pass %d: %v", i, err)
				}
			}
			if err := CheckSchema(ctx, pool); err != nil {
				t.Fatal(err)
			}
			var traffic int64
			var port, version int
			var ingress, egress *int64
			err = pool.QueryRow(ctx, `SELECT r.listen_port,r.ingress_group_id,r.egress_group_id,u.traffic,(SELECT max(version) FROM schema_version) FROM rules r JOIN rule_usage u ON u.rule_id=r.id WHERE r.id=1`).Scan(&port, &ingress, &egress, &traffic, &version)
			if err != nil || traffic != 123 || port != 24001 || version != 30 || egress != nil {
				t.Fatal("upgrade lost rule or usage", err, traffic, port, version)
			}
			if kind == "fork29" && (ingress == nil || *ingress != 1) {
				t.Fatal("fork group binding lost")
			}
		})
	}
}
