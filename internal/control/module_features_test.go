package control

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestModuleSwitchesSaveAndGuardPanel(t *testing.T) {
	f := newSecurityFixture(t)
	ctx := context.Background()
	if _, err := f.p.Exec(ctx, "UPDATE site_settings SET config=config-'features' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	siteFeatures := func() ModuleFeatures {
		t.Helper()
		w := f.request(0, "site", "GET", nil)
		securityStatus(t, w, http.StatusOK)
		var site struct {
			Features ModuleFeatures `json:"features"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &site); err != nil {
			t.Fatal(err)
		}
		return site.Features
	}
	if siteFeatures() != defaultModuleFeatures() {
		t.Fatal("missing switches must retain the existing behavior")
	}

	settings := defaultSettings()
	settings.Features = ModuleFeatures{Users: true}
	settings.RegistrationEnabled = true
	settings.RegistrationEmailVerification = false
	settings.CaptchaMode = "off"
	settings.ReferralEnabled = true
	settings.ForceInvite = true
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", settings), http.StatusOK)
	if siteFeatures() != settings.Features {
		t.Fatal("public switches do not match the saved values")
	}
	for _, tc := range []struct {
		actor        int64
		method, path string
	}{
		{f.user, "GET", "wallet"},
		{f.user, "POST", "shop/purchase"},
		{f.user, "PUT", "profile/auto-renew"},
		{f.admin, "GET", "admin/orders"},
		{f.admin, "POST", "admin/payment-methods"},
		{f.user, "GET", "referrals"},
		{f.user, "POST", "tickets"},
		{f.admin, "GET", "admin/tickets"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			securityStatus(t, f.request(tc.actor, tc.path, tc.method, map[string]any{}), http.StatusForbidden)
		})
	}
	var invite string
	if err := f.p.QueryRow(ctx, "SELECT invite_code FROM users WHERE id=$1", f.other).Scan(&invite); err != nil {
		t.Fatal(err)
	}
	email := Secret()[:12] + "@example.test"
	t.Cleanup(func() { _, _ = f.p.Exec(ctx, "DELETE FROM users WHERE username=$1", email) })
	securityStatus(t, f.request(0, "register", "POST", map[string]string{
		"email": email, "password": "fixture-module-password", "confirm_password": "fixture-module-password", "invite_code": invite,
	}), http.StatusCreated)
	var inviter *int64
	if err := f.p.QueryRow(ctx, "SELECT inviter_id FROM users WHERE username=$1", email).Scan(&inviter); err != nil || inviter != nil {
		t.Fatal("disabled invitations still bind an inviter", err)
	}

	var legacy map[string]any
	raw, _ := json.Marshal(settings)
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "features")
	legacy["site_name"] = "Fixture"
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", legacy), http.StatusOK)
	if siteFeatures() != settings.Features {
		t.Fatal("legacy save re-enabled disabled switches")
	}
	legacy["features"] = map[string]bool{"users": false}
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", legacy), http.StatusOK)
	if siteFeatures() != (ModuleFeatures{}) {
		t.Fatal("partial switch update overwrote other switches")
	}
	securityStatus(t, f.request(f.user, "me", "GET", nil), http.StatusUnauthorized)
	securityStatus(t, f.request(0, "register", "POST", nil), http.StatusForbidden)
	securityStatus(t, f.request(f.admin, "admin/settings", "GET", nil), http.StatusOK)
	securityStatus(t, f.request(f.admin, "admin/users", "GET", nil), http.StatusOK)
	securityStatus(t, f.request(f.admin, "admin/plans", "GET", nil), http.StatusOK)

	password := "fixture-only-module-switch-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   int64
		want int
	}{{f.user, http.StatusForbidden}, {f.admin, http.StatusOK}} {
		var username string
		if err := f.p.QueryRow(ctx, "UPDATE users SET password_hash=$2 WHERE id=$1 RETURNING username", tc.id, string(hash)).Scan(&username); err != nil {
			t.Fatal(err)
		}
		securityStatus(t, f.request(0, "login", "POST", map[string]string{"username": username, "password": password}), tc.want)
	}
	legacy["features"] = map[string]bool{"invites": true, "users": true}
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", legacy), http.StatusOK)
	registration := f.request(0, "registration", "GET", nil)
	securityStatus(t, registration, http.StatusOK)
	var cfg struct {
		Enabled     bool `json:"enabled"`
		ForceInvite bool `json:"force_invite"`
	}
	if err := json.Unmarshal(registration.Body.Bytes(), &cfg); err != nil || !cfg.Enabled || !cfg.ForceInvite {
		t.Fatal("registration and invite configuration did not resume", err)
	}
	securityStatus(t, f.request(f.user, "me", "GET", nil), http.StatusOK)
}

func TestCommerceSwitchPausesRenewalsAndInvitesPauseRewards(t *testing.T) {
	f, now := prepareAutoRenew(t)
	ctx := context.Background()
	if _, err := f.p.Exec(ctx, "UPDATE users SET inviter_id=$2 WHERE id=$1", f.user, f.other); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.ReferralEnabled = true
	settings.Features.Commerce = false
	settings.Features.Invites = false
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", settings), http.StatusOK)
	if err := f.s.renewDueSubscriptions(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := f.s.renewOneSubscription(ctx, f.user, now); err != nil {
		t.Fatal(err)
	}
	var balance int64
	if err := f.p.QueryRow(ctx, "SELECT balance_cents FROM wallet_accounts WHERE user_id=$1", f.user).Scan(&balance); err != nil || balance != 5000 {
		t.Fatal("disabled commerce charged a renewal", balance, err)
	}
	settings.Features.Commerce = true
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", settings), http.StatusOK)
	if err := f.s.renewDueSubscriptions(ctx, now); err != nil {
		t.Fatal(err)
	}
	var rewards int
	if err := f.p.QueryRow(ctx, "SELECT balance_cents,(SELECT count(*) FROM referral_rewards WHERE invitee_id=$1) FROM wallet_accounts WHERE user_id=$1", f.user).Scan(&balance, &rewards); err != nil || balance != 4000 || rewards != 0 {
		t.Fatal("renewal did not resume independently of invite rewards", balance, rewards, err)
	}
}

func TestCommerceSwitchAllowsOutstandingPaymentSettlement(t *testing.T) {
	f, method, config := paymentFixture(t)
	order := createPayment(t, f, method, "10.00", Secret())
	settings := defaultSettings()
	settings.Features.Commerce = false
	securityStatus(t, f.request(f.admin, "admin/settings", "PUT", settings), http.StatusOK)
	securityStatus(t, notifyPayment(f, method, paymentValues(config, order, "fixture-paused-commerce"), true), http.StatusOK)
	var balance int64
	if err := f.p.QueryRow(context.Background(), "SELECT balance_cents FROM wallet_accounts WHERE user_id=$1", f.user).Scan(&balance); err != nil || balance != 1000 {
		t.Fatal("pending payment did not settle while commerce was off", balance, err)
	}
}
