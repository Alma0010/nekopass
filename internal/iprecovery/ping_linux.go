//go:build linux

package iprecovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"syscall"
	"time"
)

func Ping(ctx context.Context, target string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if address := net.ParseIP(target); address == nil || address.To4() == nil {
		return false, errors.New("检测 IPv4 地址无效")
	}
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	raw := false
	if err != nil {
		conn, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		raw = true
	}
	if err != nil {
		return false, errors.New("无法打开 ICMP socket，请检查节点 ping 权限")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	nonce := make([]byte, 24)
	if _, err = rand.Read(nonce); err != nil {
		return false, errors.New("无法生成 ICMP 检测数据")
	}
	id := int(nonce[0])<<8 | int(nonce[1])
	packet, err := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: id, Seq: 1, Data: nonce}}).Marshal(nil)
	if err != nil {
		return false, errors.New("无法创建 ICMP 检测数据")
	}
	var destination net.Addr = &net.UDPAddr{IP: net.ParseIP(target)}
	if raw {
		destination = &net.IPAddr{IP: net.ParseIP(target)}
	}
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return false, errors.New("无法设置 ICMP 检测超时")
	}
	if _, err = conn.WriteTo(packet, destination); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETDOWN) {
			return false, nil
		}
		return false, errors.New("无法发送 ICMP 检测，请检查节点网络或权限")
	}
	buffer := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return false, nil
			}
			return false, errors.New("ICMP 检测读取失败")
		}
		address := peer.String()
		if host, _, e := net.SplitHostPort(address); e == nil {
			address = host
		}
		if address != target {
			continue
		}
		message, err := icmp.ParseMessage(1, buffer[:n])
		if err != nil || message.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := message.Body.(*icmp.Echo)
		if ok && echo.Seq == 1 && bytes.Equal(echo.Data, nonce) && (!raw || echo.ID == id) {
			return true, nil
		}
	}
}
