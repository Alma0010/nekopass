package padding

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

type memoryConn struct {
	bytes.Buffer
	writes []int
}

func (c *memoryConn) Write(b []byte) (int, error) {
	c.writes = append(c.writes, len(b))
	return c.Buffer.Write(b)
}
func (c *memoryConn) Close() error                     { return nil }
func (c *memoryConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *memoryConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }

func TestSchemeValidationAndAuth(t *testing.T) {
	s, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, e := s.AuthSize(); e != nil || n != 30 || !s.ValidAuth(n) || s.ValidAuth(2049) {
		t.Fatal("invalid default authentication padding")
	}
	for _, lines := range [][]string{{"1=20-30"}, {"stop=-1"}, {"stop=65"}, {"stop=8", "stop=9"}, {"stop=8", "0=c"}, {"stop=8", "0=30-30,40-40"}, {"stop=8", "0=2049-3000"}, {"stop=8", "1=0-20"}, {"stop=8", "1=1-16385"}, {"stop=8", "garbage"}, {"stop=8", "01=2-3"}} {
		if _, err := Parse(lines); err == nil {
			t.Fatal("unsafe or malformed policy accepted", lines)
		}
	}
	s, err = Parse([]string{"stop=0"})
	if err != nil || !s.ValidAuth(0) || s.ValidAuth(1) {
		t.Fatal("empty authentication padding")
	}
}
func TestShapeAndConditionalStopPreserveExactData(t *testing.T) {
	s, err := Parse([]string{"stop=3", "1=100-100,c,200-200", "2=80-80"})
	if err != nil {
		t.Fatal(err)
	}
	base := &memoryConn{}
	c := NewConn(base, s)
	if n, err := c.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if len(base.writes) != 1 || base.writes[0] != 100 {
		t.Fatal("conditional stop did not limit writes", base.writes)
	}
	wire := base.Bytes()
	if wire[0] != 2 || binary.BigEndian.Uint32(wire[1:5]) != 1 || binary.BigEndian.Uint16(wire[5:7]) != 3 || string(wire[7:10]) != "abc" || wire[10] != 0 || binary.BigEndian.Uint16(wire[15:17]) != 83 {
		t.Fatal("incorrect data/padding frame encoding")
	}
	if n, err := c.Write([]byte("def")); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(NewConn(base, s))
	if err != nil || string(got) != "abcdef" {
		t.Fatal("padding leaked or data changed", err)
	}
	if _, err = c.Write([]byte("late")); err == nil {
		t.Fatal("write after half-close accepted")
	}
}
func TestLargeTransferAndHalfClose(t *testing.T) {
	s, _ := Parse(Defaults())
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	left.SetDeadline(time.Now().Add(5 * time.Second))
	right.SetDeadline(time.Now().Add(5 * time.Second))
	a, b := NewConn(left, s), NewConn(right, s)
	payload := bytes.Repeat([]byte{0, 1, 255, 10}, 256<<10)
	done := make(chan error, 1)
	go func() {
		got, err := io.ReadAll(b)
		if err == nil && !bytes.Equal(got, payload) {
			err = io.ErrNoProgress
		}
		if err == nil {
			_, err = b.Write([]byte("reply-after-fin"))
		}
		if err == nil {
			err = b.CloseWrite()
		}
		done <- err
	}()
	if n, err := a.Write(payload); n != len(payload) || err != nil {
		t.Fatal(n, err)
	}
	if err := a.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(a)
	if err != nil || string(got) != "reply-after-fin" {
		t.Fatal("half-close lost reverse data", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRejectMalformedAndFloodFrames(t *testing.T) {
	s, _ := Parse(nil)
	for _, h := range [][7]byte{header(99, 1), header(dataCommand, 0), header(dataCommand, 65535), header(wasteCommand, 0), header(finCommand, 1)} {
		base := &memoryConn{}
		base.Buffer.Write(h[:])
		if _, err := NewConn(base, s).Read(make([]byte, 1)); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	base := &memoryConn{}
	h := header(wasteCommand, 1)
	for i := 0; i < maxFrames+1; i++ {
		base.Buffer.Write(h[:])
		base.Buffer.WriteByte(0)
	}
	if _, err := NewConn(base, s).Read(make([]byte, 1)); err != errFrame {
		t.Fatal("padding flood not bounded", err)
	}
}
func FuzzPaddingRead(f *testing.F) {
	s, _ := Parse(nil)
	h := header(dataCommand, 1)
	f.Add(append(h[:], byte(42)))
	fin := header(finCommand, finMin)
	f.Add(append(fin[:], make([]byte, finMin)...))
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		base := &memoryConn{}
		base.Buffer.Write(input)
		c := NewConn(base, s)
		_, _ = io.Copy(io.Discard, c)
	})
}
