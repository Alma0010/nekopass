package agent

import (
	"encoding/binary"
	"errors"
	"io"
	"net"

	"github.com/nekopass/nekopass/internal/padding"
	pb "github.com/nekopass/nekopass/internal/protocol"
)

var paddedTunnelMagic = [4]byte{'N', 'P', 'T', 3}

func rulePadding(r *pb.Rule) (*padding.Scheme, error) {
	if r.TunnelProtocol != "tls_tcp" || !r.Tls.GetPaddingEnabled() {
		return nil, nil
	}
	return padding.Parse(r.Tls.PaddingScheme)
}
func nodePadding(n *pb.NodeConfig) (*padding.Scheme, error) {
	if n == nil || n.TunnelProtocol != "tls_tcp" || !n.Tls.GetPaddingEnabled() {
		return nil, nil
	}
	return padding.Parse(n.Tls.PaddingScheme)
}
func writePaddedHello(c net.Conn, r *pb.Rule, target string, s *padding.Scheme) error {
	if len(r.TunnelToken) != 64 || len(target) < 1 || len(target) > 512 {
		return errors.New("invalid tunnel configuration")
	}
	n, err := s.AuthSize()
	if err != nil {
		return err
	}
	b := make([]byte, 86+len(target)+2+n)
	copy(b, paddedTunnelMagic[:])
	binary.BigEndian.PutUint64(b[4:12], uint64(r.IngressNodeId))
	binary.BigEndian.PutUint64(b[12:20], uint64(r.Id))
	copy(b[20:84], r.TunnelToken)
	binary.BigEndian.PutUint16(b[84:86], uint16(len(target)))
	copy(b[86:], target)
	binary.BigEndian.PutUint16(b[86+len(target):], uint16(n))
	return writeFullPadding(c, b)
}
func writeFullPadding(c net.Conn, b []byte) error {
	n, err := c.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}
func readAuthPadding(c net.Conn, s *padding.Scheme) error {
	var h [2]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint16(h[:]))
	if !s.ValidAuth(n) {
		return errors.New("invalid authentication padding length")
	}
	_, err := io.CopyN(io.Discard, c, int64(n))
	return err
}
