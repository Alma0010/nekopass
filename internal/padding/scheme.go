// Package padding implements bounded AnyTLS-style write shaping for Nekopass
// raw TCP/TLS tunnels. The policy syntax is shared, not the AnyTLS wire protocol.
package padding

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

const MaxAuth = 2048
const MaxRecord = 16384
const MaxBudget = 256 << 10
const maxFrames = 2048

var defaultLines = [...]string{"stop=8", "0=30-30", "1=100-400", "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000", "3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000"}

func Defaults() []string { return append([]string(nil), defaultLines[:]...) }

type interval struct{ low, high int }
type Scheme struct {
	stop  int
	rules map[int][]interval
}

func Parse(lines []string) (*Scheme, error) {
	if len(lines) == 0 {
		lines = Defaults()
	}
	if len(lines) > 65 || len(strings.Join(lines, "\n")) > 8192 {
		return nil, fmt.Errorf("padding policy too large")
	}
	s := &Scheme{stop: -1, rules: map[int][]interval{}}
	seen := map[string]bool{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || seen[key] {
			return nil, fmt.Errorf("padding line %d: invalid or repeated key", i+1)
		}
		seen[key] = true
		if key == "stop" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 0 || n > 64 {
				return nil, fmt.Errorf("padding stop must be 0-64")
			}
			s.stop = n
			continue
		}
		id, e := strconv.Atoi(key)
		if e != nil || id < 0 || id > 63 || strconv.Itoa(id) != key {
			return nil, fmt.Errorf("padding line %d: invalid write index", i+1)
		}
		parts := strings.Split(value, ",")
		if len(parts) > 16 {
			return nil, fmt.Errorf("padding line %d: too many fragments", i+1)
		}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "c" && id != 0 {
				s.rules[id] = append(s.rules[id], interval{-1, -1})
				continue
			}
			a, b, ok := strings.Cut(part, "-")
			lo, e1 := strconv.Atoi(a)
			hi, e2 := strconv.Atoi(b)
			if lo > hi {
				lo, hi = hi, lo
			}
			limit := MaxRecord
			if id == 0 {
				limit = MaxAuth
			}
			if !ok || e1 != nil || e2 != nil || lo < 1 || hi > limit || id == 0 && len(parts) != 1 {
				return nil, fmt.Errorf("padding line %d: expected lengths 1-%d", i+1, limit)
			}
			s.rules[id] = append(s.rules[id], interval{lo, hi})
		}
	}
	if s.stop < 0 {
		return nil, fmt.Errorf("padding policy requires stop")
	}
	return s, nil
}

func randomSize(r interval) (int, error) {
	if r.low == r.high {
		return r.low, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(r.high-r.low)))
	if err != nil {
		return 0, err
	}
	return r.low + int(n.Int64()), nil
}
func (s *Scheme) AuthSize() (int, error) {
	if len(s.rules[0]) == 0 {
		return 0, nil
	}
	return randomSize(s.rules[0][0])
}
func (s *Scheme) ValidAuth(n int) bool {
	r := s.rules[0]
	return n >= 0 && n <= MaxAuth && (len(r) == 0 && n == 0 || len(r) == 1 && n >= r[0].low && n <= r[0].high)
}
