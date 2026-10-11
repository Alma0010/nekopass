package iprecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

const IntervalSeconds = 5

type Config struct {
	Enabled          bool   `json:"enabled"`
	TargetIP         string `json:"target_ip"`
	FailureThreshold int    `json:"failure_threshold"`
	CooldownSeconds  int    `json:"cooldown_seconds"`
	InstanceType     string `json:"instance_type"`
	AuthToken        string `json:"auth_token"`
	ShareGroupToken  string `json:"share_group_token"`
	AccountID        string `json:"account_id"`
	RegionName       string `json:"region_name"`
	InstanceID       string `json:"instance_id"`
	StaticIP         bool   `json:"static_ip"`
	Generation       int64  `json:"-"`
	ResetSequence    int64  `json:"-"`
}

func DefaultConfig() Config {
	return Config{FailureThreshold: 6, CooldownSeconds: 300, InstanceType: "ec2"}
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,255}$`)

func PublicIPv4(value string) bool {
	a, err := netip.ParseAddr(value)
	return err == nil && a.Is4() && a.IsGlobalUnicast() && !a.IsPrivate()
}
func (c *Config) Normalize() error {
	c.TargetIP = strings.TrimSpace(c.TargetIP)
	c.AuthToken = strings.TrimSpace(c.AuthToken)
	c.ShareGroupToken = strings.TrimSpace(c.ShareGroupToken)
	c.AccountID = strings.TrimSpace(c.AccountID)
	c.RegionName = strings.TrimSpace(c.RegionName)
	c.InstanceID = strings.TrimSpace(c.InstanceID)
	if c.InstanceType == "" {
		c.InstanceType = "ec2"
	}
	if c.FailureThreshold == 0 {
		c.FailureThreshold = 6
	}
	if c.CooldownSeconds == 0 {
		c.CooldownSeconds = 300
	}
	if c.InstanceType != "ec2" && c.InstanceType != "lightsail" {
		return errors.New("实例类型须为 EC2 或 Lightsail")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 720 || c.CooldownSeconds < 30 || c.CooldownSeconds > 86400 {
		return errors.New("连续失败次数须为 1–720，冷却时间须为 30–86400 秒")
	}
	if c.TargetIP != "" && !PublicIPv4(c.TargetIP) {
		return errors.New("检测目标须为公网 IPv4 地址")
	}
	for _, v := range []string{c.AccountID, c.RegionName, c.InstanceID} {
		if v != "" && !identifier.MatchString(v) {
			return errors.New("账号、地区或实例标识格式无效")
		}
	}
	for _, v := range []string{c.AuthToken, c.ShareGroupToken} {
		if len(v) > 4096 {
			return errors.New("小助理凭证过长")
		}
		for _, r := range v {
			if r < 33 || r > 126 {
				return errors.New("小助理凭证不能包含空白或非 ASCII 字符")
			}
		}
	}
	if c.Enabled && (c.TargetIP == "" || c.AccountID == "" && c.ShareGroupToken == "" || c.RegionName == "" || c.InstanceID == "" || c.AuthToken == "" && c.ShareGroupToken == "") {
		return errors.New("请填写检测 IP、实例信息以及小助理认证 Token 或分享组 Token")
	}
	return nil
}

// ResourceKey intentionally excludes credentials and config revision. Updating
// a token or threshold must not forget an in-flight cloud operation.
func (c Config) ResourceKey() string {
	b, _ := json.Marshal([]string{c.InstanceType, c.AccountID, c.RegionName, c.InstanceID})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
