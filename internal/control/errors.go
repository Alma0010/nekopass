package control

import (
	"errors"
	"net"

	"github.com/jackc/pgx/v5/pgconn"
)

// Keep validation messages useful while preventing transport/SQL errors from
// returning connection details, database values or credentials to a client.
func publicOperationError(err error) string {
	var sqlError *pgconn.PgError
	var connectError *pgconn.ConnectError
	var parseError *pgconn.ParseConfigError
	var networkError net.Error
	if errors.As(err, &sqlError) && sqlError.ConstraintName == "rule_sni_names_pkey" {
		return "此 SNI 已被其他规则占用（暂停规则仍保留 SNI）"
	}
	if errors.As(err, &sqlError) || errors.As(err, &connectError) || errors.As(err, &parseError) || errors.As(err, &networkError) {
		return "操作失败：请检查资源约束或稍后重试"
	}
	return err.Error()
}
