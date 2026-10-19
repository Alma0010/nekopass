package release

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// NormalizeDownloadBase validates a directory URL before appending release paths.
func NormalizeDownloadBase(raw string) (string, error) {
	if !strings.HasPrefix(raw, "https://") || len(raw) > 2048 || strings.ContainsAny(raw, "\\?#") || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("invalid release download base")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid release download base")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid download port")
		}
	}
	return strings.TrimRight(raw, "/"), nil
}

// AtLeast requires a real installed release, so dev builds cannot confirm updates.
func AtLeast(current, target string) bool {
	return ValidVersion(current) && ValidVersion(target) && !Newer(target, current)
}
