//go:build linux

package iprecovery

import (
	"context"
	"testing"
	"time"
)

func TestLinuxPingLoopbackAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reachable, err := Ping(ctx, "127.0.0.1")
	if err != nil || !reachable {
		t.Fatal("native ICMP loopback failed", err)
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	before := time.Now()
	reachable, err = Ping(stopped, "127.0.0.1")
	if reachable || err == nil || time.Since(before) > time.Second {
		t.Fatal("canceled ICMP probe continued", err)
	}
}
