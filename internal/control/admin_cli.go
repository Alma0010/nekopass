package control

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// These operations are local CLI commands, never HTTP endpoints.
func RandomAdministratorPassword() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func ResetAdministratorPassword(ctx context.Context, p *pgxpool.Pool, username string) (string, string, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "SELECT id,username,enabled FROM users WHERE is_admin AND ($1='' OR username=$1) ORDER BY id FOR UPDATE", username)
	if err != nil {
		return "", "", err
	}
	var id int64
	var name string
	var enabled bool
	count := 0
	for rows.Next() {
		if err = rows.Scan(&id, &name, &enabled); err != nil {
			rows.Close()
			return "", "", err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", "", err
	}
	if count == 0 {
		return "", "", errors.New("管理员不存在")
	}
	if count != 1 {
		return "", "", errors.New("存在多个管理员，请使用 -admin-name 指定账号")
	}
	if !enabled {
		return "", "", errors.New("管理员已停用，密码重置不会重新启用账号")
	}
	password, err := RandomAdministratorPassword()
	if err != nil {
		return "", "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET password_hash=$2 WHERE id=$1", id, string(hash)); err != nil {
		return "", "", err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
		return "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return name, password, nil
}

// Only installer-owned fields can be changed by init-settings. Missing fields
// retain stored values or current defaults, including registration and ACLs.
type InstallationSettings struct {
	SiteName            *string `json:"site_name"`
	PanelURL            *string `json:"panel_url"`
	AgentHost           *string `json:"agent_host"`
	AgentPort           *int    `json:"agent_port"`
	AgentTransport      *string `json:"agent_transport"`
	InstallerURL        *string `json:"installer_url"`
	ReleaseBaseURL      *string `json:"release_base_url"`
	AgentVersion        *string `json:"agent_version"`
	InstallTokenMinutes *int    `json:"install_token_minutes"`
}

func InitializeInstallationSettings(ctx context.Context, p *pgxpool.Pool, input InstallationSettings) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM site_settings WHERE id=1 FOR UPDATE"); err != nil {
		return err
	}
	v, err := readSystemSettings(ctx, tx)
	if err != nil {
		return err
	}
	if input.SiteName != nil {
		v.SiteName = *input.SiteName
	}
	if input.PanelURL != nil {
		v.PanelURL = *input.PanelURL
	}
	if input.AgentHost != nil {
		v.AgentHost = *input.AgentHost
	}
	if input.AgentPort != nil {
		v.AgentPort = *input.AgentPort
	}
	if input.AgentTransport != nil {
		v.AgentTransport = *input.AgentTransport
	}
	if input.InstallerURL != nil {
		v.InstallerURL = *input.InstallerURL
	}
	if input.ReleaseBaseURL != nil {
		v.ReleaseBaseURL = *input.ReleaseBaseURL
	}
	if input.AgentVersion != nil {
		v.AgentVersion = *input.AgentVersion
	}
	if input.InstallTokenMinutes != nil {
		v.InstallTokenMinutes = *input.InstallTokenMinutes
	}
	if err := validateSettings(v); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE site_settings SET config=$1,updated_at=now() WHERE id=1", data); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
