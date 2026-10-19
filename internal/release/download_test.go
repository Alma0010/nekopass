package release

import "testing"

func TestDownloadBaseAndInstalledVersion(t *testing.T) {
	proxy := "https://v4.gh-proxy.org/https://github.com/Alma0010/nekopass/releases/download"
	for _, base := range []string{DownloadBase, proxy, "https://oss.example/releases", "https://[::1]:443/releases"} {
		got, err := NormalizeDownloadBase(base + "/")
		if err != nil || got != base {
			t.Fatalf("base %q: %q, %v", base, got, err)
		}
	}
	for _, base := range []string{"", "http://proxy.example", "https://u:p@proxy.example", "https://proxy.example/x?token=x", "https://proxy.example/x#x", "https://proxy.example:99999/x", "https://proxy.example/space here", "https://proxy.example/line\nnext", "https://proxy.example/back\\slash"} {
		if _, err := NormalizeDownloadBase(base); err == nil {
			t.Fatalf("invalid base accepted: %q", base)
		}
	}
	for _, c := range []struct {
		current, target string
		want            bool
	}{
		{"v0.19.3", "v0.19.2", true}, {"v0.19.2", "v0.19.2", true}, {"v0.19.1", "v0.19.2", false}, {"dev", "v0.19.2", false}, {"v0.19.2", "invalid", false},
	} {
		if AtLeast(c.current, c.target) != c.want {
			t.Fatal(c)
		}
	}
}
