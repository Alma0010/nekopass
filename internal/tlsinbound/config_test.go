package tlsinbound

import "testing"

func TestSNIAndWildcardCertificateBoundaries(t *testing.T) {
	names, err := Names([]string{"App.Example.com.", "app.example.com", "例子.example.com"})
	if err != nil || len(names) != 2 || names[0] != "app.example.com" {
		t.Fatal(names, err)
	}
	for _, value := range []string{"127.0.0.1", "*.example.com", "a..example.com", "https://example.com", "a.example.com:443", "-bad.example.com"} {
		if _, err := Name(value); err == nil {
			t.Fatal("invalid SNI accepted", value)
		}
	}
	for _, test := range []struct {
		name string
		want bool
	}{{"a.example.com", true}, {"example.com", false}, {"a.b.example.com", false}, {"a.notexample.com", false}} {
		if CoversDomain("*.example.com", test.name) != test.want {
			t.Fatal("wildcard scope incorrect", test.name)
		}
	}
	cfg := DefaultConfig()
	cfg.Name = "Shared"
	cfg.TLS.ServerName = "*.example.com"
	cfg.AllowedSNI = []string{".example.com"}
	if err = cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.Allows("example.com") || cfg.Allows("badexample.com") || !cfg.Allows("a.example.com") {
		t.Fatal("suffix boundary incorrect")
	}
	cfg.TLS.CertificateMode = "acme_http"
	if cfg.Normalize() == nil {
		t.Fatal("wildcard HTTP issuance accepted")
	}
}
