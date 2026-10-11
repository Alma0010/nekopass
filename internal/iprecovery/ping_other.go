//go:build !linux

package iprecovery

import (
	"context"
	"errors"
)

func Ping(context.Context, string) (bool, error) {
	return false, errors.New("自动换 IP 的 ICMP 检测需要 Linux 节点")
}
