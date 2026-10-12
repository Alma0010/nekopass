package control

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestInstallationSettingsUseDefaultsAndPreserveExistingOptions(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	var saved []byte
	if err := p.QueryRow(ctx, "SELECT config FROM site_settings WHERE id=1").Scan(&saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Exec(ctx, "UPDATE site_settings SET config=$1 WHERE id=1", saved) })
	if _, err := p.Exec(ctx, "UPDATE site_settings SET config='{}' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	// This is the installer payload: deliberately no CAPTCHA/referral/SMTP fields.
	var input InstallationSettings
	if err := json.Unmarshal([]byte(`{"site_name":"Fixture panel","panel_url":"http://127.0.0.1:18080","agent_host":"127.0.0.1","agent_port":19443,"agent_transport":"plain","installer_url":"https://example.test/install-agent.sh","release_base_url":"https://example.test/releases","agent_version":"latest","install_token_minutes":30}`), &input); err != nil {
		t.Fatal(err)
	}
	if err := InitializeInstallationSettings(ctx, p, input); err != nil {
		t.Fatal("fresh installation failed", err)
	}
	current, err := readSystemSettings(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defaults := defaultSettings()
	if current.CaptchaMode != defaults.CaptchaMode || current.ReferralMode != defaults.ReferralMode || current.ReferralRate != defaults.ReferralRate || current.SMTP != defaults.SMTP || !current.RegistrationEmailVerification || !reflect.DeepEqual(current.TargetDenyCIDRs, defaults.TargetDenyCIDRs) {
		t.Fatal("omitted defaults were lost")
	}
	if current.SiteName != "Fixture panel" || current.AgentTransport != "plain" || current.AgentPort != 19443 {
		t.Fatal("installer configuration not applied")
	}
	// A later local init must not erase unrelated security/registration settings.
	current.CaptchaMode = "off"
	current.ReferralMode = "recurring"
	current.ReferralRate = "12.5"
	current.RegistrationEmailVerification = false
	current.ProxyTrustedCIDRs = []string{"192.0.2.0/24"}
	current.TargetDenyCIDRs = []string{"127.0.0.0/8"}
	data, _ := json.Marshal(current)
	if _, err = p.Exec(ctx, "UPDATE site_settings SET config=$1 WHERE id=1", data); err != nil {
		t.Fatal(err)
	}
	if err = InitializeInstallationSettings(ctx, p, InstallationSettings{}); err != nil {
		t.Fatal(err)
	}
	retained, err := readSystemSettings(ctx, p)
	if err != nil || !reflect.DeepEqual(current, retained) {
		t.Fatal("unrelated stored settings were overwritten", err)
	}
	// Empty optional URLs are intentional; invalid explicit values still fail.
	empty := ""
	if err = InitializeInstallationSettings(ctx, p, InstallationSettings{InstallerURL: &empty}); err != nil {
		t.Fatal(err)
	}
	port := 0
	if err = InitializeInstallationSettings(ctx, p, InstallationSettings{AgentPort: &port}); err == nil {
		t.Fatal("invalid explicit installer port accepted")
	}
	after, err := readSystemSettings(ctx, p)
	if err != nil || after.AgentPort != 19443 || after.InstallerURL != "" {
		t.Fatal("failed validation changed saved settings", err)
	}
}

func TestLocalAdministratorResetScopeAndRevocation(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	a, _ := testUser(t, p, 1<<20, 10, true)
	b, _ := testUser(t, p, 1<<20, 10, true)
	u, _ := testUser(t, p, 1<<20, 10, false)
	var aName, bName, uName, oldA, oldB string
	if err := p.QueryRow(ctx, "SELECT username,password_hash FROM users WHERE id=$1", a).Scan(&aName, &oldA); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, "SELECT username,password_hash FROM users WHERE id=$1", b).Scan(&bName, &oldB); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, "SELECT username FROM users WHERE id=$1", u).Scan(&uName); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{a, b} {
		if _, err := p.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')", Hash(Secret()), id); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"", uName, Secret()} {
		if _, _, err := ResetAdministratorPassword(ctx, p, name); err == nil {
			t.Fatal("ambiguous/non-admin/missing account accepted")
		}
	}
	name, password, err := ResetAdministratorPassword(ctx, p, aName)
	if err != nil {
		t.Fatal(err)
	}
	if name != aName || len(password) != 32 {
		t.Fatal("invalid generated credential")
	}
	var currentA, currentB string
	var aSessions, bSessions int
	if err = p.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", a).Scan(&currentA); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", b).Scan(&currentB); err != nil {
		t.Fatal(err)
	}
	if currentA == oldA || currentB != oldB || bcrypt.CompareHashAndPassword([]byte(currentA), []byte(password)) != nil {
		t.Fatal("password scope/hash incorrect")
	}
	p.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", a).Scan(&aSessions)
	p.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", b).Scan(&bSessions)
	if aSessions != 0 || bSessions != 1 {
		t.Fatal("session revocation affected wrong account")
	}
	if _, err = p.Exec(ctx, "UPDATE users SET enabled=false WHERE id=$1", b); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ResetAdministratorPassword(ctx, p, bName); err == nil {
		t.Fatal("disabled admin reactivated")
	}
}
