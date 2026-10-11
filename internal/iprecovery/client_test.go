package iprecovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixtureConfig() Config {
	c := DefaultConfig()
	c.Enabled = true
	c.TargetIP = "1.1.1.1"
	c.AuthToken = "fixture-token"
	c.AccountID = "fixture-account"
	c.RegionName = "ap-southeast-1"
	c.InstanceID = "i-fixture"
	c.Generation = 1
	return c
}
func TestConfigRejectsInvalidTargetsAndCredentials(t *testing.T) {
	valid := fixtureConfig()
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.TargetIP = "127.0.0.1" }, func(c *Config) { c.TargetIP = "169.254.169.254" }, func(c *Config) { c.TargetIP = "192.168.1.1" }, func(c *Config) { c.TargetIP = "::1" }, func(c *Config) { c.TargetIP = "example.com" }, func(c *Config) { c.AuthToken = "" }, func(c *Config) { c.AuthToken = "fixture-token\r\nInjected:yes" }, func(c *Config) { c.InstanceID = "../delete" }, func(c *Config) { c.FailureThreshold = -1 }, func(c *Config) { c.CooldownSeconds = 5 }} {
		c := valid
		mutate(&c)
		if c.Normalize() == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	key := valid.ResourceKey()
	valid.AuthToken = "updated-token"
	valid.Generation++
	valid.FailureThreshold++
	if valid.ResourceKey() != key {
		t.Fatal("credential edit lost operation identity")
	}
	disabled := DefaultConfig()
	if disabled.Normalize() != nil {
		t.Fatal("disabled default configuration rejected")
	}
	shared := fixtureConfig()
	shared.AuthToken = ""
	shared.ShareGroupToken = "fixture-share"
	shared.AccountID = ""
	if shared.Normalize() != nil {
		t.Fatal("sharing authentication rejected")
	}
}
func TestCloudRequestUsesHeadersAndNeverVendorGFWDetection(t *testing.T) {
	for _, kind := range []string{"ec2", "lightsail"} {
		t.Run(kind, func(t *testing.T) {
			cfg := fixtureConfig()
			cfg.InstanceType = kind
			var changes int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Auth-Token") != cfg.AuthToken || r.Header.Get("X-Account-Id") != cfg.AccountID || r.Header.Get("X-Region-Name") != cfg.RegionName {
					t.Error("missing identity headers")
				}
				if r.URL.Path == "/filter-tasks" {
					var body struct {
						Tags []string `json:"tags"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					if len(body.Tags) != 1 || body.Tags[0] != "fixture-task" {
						t.Error("wrong task filter")
					}
					fmt.Fprint(w, `[{"tag":"fixture-task","status":"success","endedAt":"2026-10-11T00:00:00Z"}]`)
					return
				}
				if r.URL.Path != instancePath(cfg) && r.URL.Path != instancePath(cfg)+"/ip-address" {
					t.Error("unexpected path")
				}
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"ipAddress":"1.1.1.1"}`)
					return
				}
				if r.Method != http.MethodPatch {
					t.Error("unexpected mutation method")
				}
				changes++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["static"] != false || kind == "ec2" && body["gfw_blocked_check"] != false || len(body) != map[string]int{"ec2": 2, "lightsail": 1}[kind] {
					t.Error("unexpected recovery settings", body)
				}
				fmt.Fprint(w, `{"tag":"fixture-task","status":"pending"}`)
			}))
			defer server.Close()
			client := NewClient().(*Client)
			client.baseURL = server.URL
			client.http = server.Client()
			if ip, err := client.IP(context.Background(), cfg); err != nil || ip != "1.1.1.1" {
				t.Fatal(err)
			}
			task, err := client.Change(context.Background(), cfg)
			if err != nil || changes != 1 {
				t.Fatal("change failed", err)
			}
			if kind == "ec2" && task.Tag != "fixture-task" {
				t.Fatal("task missing")
			}
			task, found, err := client.Task(context.Background(), cfg, "fixture-task")
			if err != nil || !found || !task.Ended() {
				t.Fatal("task query failed", err)
			}
		})
	}
}
func TestAPIRejectsRedirectAndNeverExposesCredential(t *testing.T) {
	cfg := fixtureConfig()
	var contacted bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { contacted = true }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer server.Close()
	client := NewClient().(*Client)
	client.baseURL = server.URL
	_, err := client.IP(context.Background(), cfg)
	if err == nil || contacted {
		t.Fatal("credentials followed redirect")
	}
	server.Close()
	_, err = client.Change(context.Background(), cfg)
	if err == nil || strings.Contains(err.Error(), cfg.AuthToken) || strings.Contains(err.Error(), server.URL) {
		t.Fatal("unsafe transport error")
	}
	apiError, ok := err.(*APIError)
	if !ok || !apiError.Ambiguous {
		t.Fatal("network error treated as definite rejection")
	}
}
