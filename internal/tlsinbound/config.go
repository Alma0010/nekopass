package tlsinbound

import (
	"crypto/x509"
	"errors"
	"golang.org/x/net/idna"
	"net/netip"
	"slices"
	"strings"
)

type Config struct {
	Name                    string   `json:"name"`
	Port                    int      `json:"port"`
	Enabled                 bool     `json:"enabled"`
	ProxyAccept             string   `json:"proxy_accept"`
	AllowedSNI              []string `json:"allowed_sni"`
	RejectEmptySNI          bool     `json:"reject_empty_sni"`
	UnknownSNIAction        string   `json:"unknown_sni_action"`
	DefaultRuleID           int64    `json:"default_rule_id"`
	MaxHandshakes           int      `json:"max_handshakes"`
	HandshakeTimeoutSeconds int      `json:"handshake_timeout_seconds"`
}
type OriginTLS struct {
	Enabled    bool     `json:"enabled"`
	ServerName string   `json:"server_name"`
	RootCA     string   `json:"root_ca"`
	ALPN       []string `json:"alpn"`
}

func DefaultConfig() Config {
	return Config{Enabled: true, Port: 443, ProxyAccept: "off", AllowedSNI: []string{}, RejectEmptySNI: true, UnknownSNIAction: "reject", MaxHandshakes: 128, HandshakeTimeoutSeconds: 5}
}
func Name(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	value, err := idna.Lookup.ToASCII(raw)
	if err != nil || value == "" || len(value) > 253 || strings.ContainsAny(value, "*/:\\@\x00\r\n") {
		return "", errors.New("SNI 须为有效域名，不含协议、端口或通配符")
	}
	if _, err = netip.ParseAddr(value); err == nil {
		return "", errors.New("SNI 必须是域名，不能填写 IP")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("SNI 域名格式无效")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("SNI 域名格式无效")
			}
		}
	}
	return value, nil
}
func Names(values []string) ([]string, error) {
	if len(values) < 1 || len(values) > 32 {
		return nil, errors.New("请填写 1–32 个 SNI 域名")
	}
	out := []string{}
	for _, v := range values {
		name, err := Name(v)
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
func ALPN(values []string) error {
	if len(values) > 8 {
		return errors.New("ALPN 最多 8 个协议")
	}
	for _, v := range values {
		if len(v) == 0 || len(v) > 255 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("ALPN 协议格式无效")
		}
	}
	return nil
}
func (c *Config) Allows(sni string) bool {
	if len(c.AllowedSNI) == 0 {
		return true
	}
	for _, suffix := range c.AllowedSNI {
		if strings.HasPrefix(suffix, ".") {
			if strings.HasSuffix(sni, suffix) && sni != suffix[1:] {
				return true
			}
		} else if sni == suffix || strings.HasSuffix(sni, "."+suffix) {
			return true
		}
	}
	return false
}
func (c *Config) Normalize() error {
	d := DefaultConfig()
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 64 {
		return errors.New("共享入口名称须为 1–64 字")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("TLS 监听端口须为 1–65535")
	}
	if c.ProxyAccept == "" {
		c.ProxyAccept = "off"
	}
	if c.ProxyAccept != "off" && c.ProxyAccept != "auto" {
		return errors.New("共享入口 PROXY 接收仅支持开启或关闭")
	}
	if c.UnknownSNIAction == "" {
		c.UnknownSNIAction = "reject"
	}
	if c.UnknownSNIAction != "reject" && c.UnknownSNIAction != "default_rule" {
		return errors.New("未知 SNI 处理方式无效")
	}
	if c.UnknownSNIAction == "default_rule" && c.DefaultRuleID < 1 {
		return errors.New("请选择管理员默认规则")
	}
	if c.UnknownSNIAction == "reject" {
		c.DefaultRuleID = 0
	}
	if c.MaxHandshakes == 0 {
		c.MaxHandshakes = d.MaxHandshakes
	}
	if c.HandshakeTimeoutSeconds == 0 {
		c.HandshakeTimeoutSeconds = d.HandshakeTimeoutSeconds
	}
	if c.MaxHandshakes < 1 || c.MaxHandshakes > 4096 || c.HandshakeTimeoutSeconds < 1 || c.HandshakeTimeoutSeconds > 30 {
		return errors.New("ClientHello 并发读取须为 1–4096，读取超时须为 1–30 秒")
	}
	if len(c.AllowedSNI) > 128 {
		return errors.New("SNI 允许范围最多 128 条")
	}
	out := []string{}
	for _, v := range c.AllowedSNI {
		v = strings.TrimSpace(v)
		dot := strings.HasPrefix(v, ".")
		n, err := Name(strings.TrimPrefix(v, "."))
		if err != nil {
			return err
		}
		if dot {
			n = "." + n
		}
		out = append(out, n)
	}
	slices.Sort(out)
	c.AllowedSNI = slices.Compact(out)

	return nil
}
func (o *OriginTLS) Normalize() error {
	if o.ServerName != "" {
		value, err := Name(o.ServerName)
		if err != nil {
			return err
		}
		o.ServerName = value
	}
	if len(o.RootCA) > 131072 {
		return errors.New("回源 CA 证书过长")
	}
	if o.RootCA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(o.RootCA)) {
			return errors.New("回源 CA 证书无效")
		}
	}
	return ALPN(o.ALPN)
}
