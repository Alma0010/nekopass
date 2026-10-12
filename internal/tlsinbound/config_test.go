package tlsinbound

import "testing"

func TestSNIAndAllowlistBoundaries(t *testing.T) {
	names, err := Names([]string{"App.Example.com.", "app.example.com", "例子.example.com"})
	if err != nil || len(names) != 2 || names[0] != "app.example.com" {
		t.Fatal(names, err)
	}
	for _, value := range []string{"127.0.0.1", "*.example.com", "a..example.com", "https://example.com", "a.example.com:443", "-bad.example.com"} {
		if _, err := Name(value); err == nil {
			t.Fatal("invalid SNI accepted", value)
		}
	}
	cfg := DefaultConfig()
	cfg.Name = "Shared"
	cfg.AllowedSNI = []string{".example.com"}
	if err = cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.Allows("example.com") || cfg.Allows("badexample.com") || !cfg.Allows("a.example.com") {
		t.Fatal("suffix boundary incorrect")
	}
}
