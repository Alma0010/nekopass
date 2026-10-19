package padding

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFINIsOneRandomPaddedWriteAfterStop(t *testing.T) {
	s, err := Parse([]string{"stop=0"})
	if err != nil {
		t.Fatal(err)
	}
	lengths := map[int]bool{}
	for i := 0; i < 64; i++ {
		base := &memoryConn{}
		c := NewConn(base, s)
		if _, err := c.Write([]byte("business")); err != nil {
			t.Fatal(err)
		}
		start, writes := base.Len(), len(base.writes)
		if err := c.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		fin := base.Bytes()[start:]
		if len(base.writes) != writes+1 || len(fin) < overhead+finMin || len(fin) > overhead+finMax || fin[0] != finCommand || binary.BigEndian.Uint32(fin[1:5]) != 1 || int(binary.BigEndian.Uint16(fin[5:7])) != len(fin)-overhead {
			t.Fatal("FIN not emitted as a single bounded padded frame", base.writes, len(fin))
		}
		lengths[len(fin)] = true
		if err := c.CloseWrite(); err != nil || base.Len() != start+len(fin) {
			t.Fatal("repeated half-close wrote another frame", err)
		}
		got, err := io.ReadAll(NewConn(base, s))
		if err != nil || string(got) != "business" {
			t.Fatal("FIN payload exposed as business bytes", string(got), err)
		}
	}
	// A fixed seven-byte FIN or an accidentally constant random size must fail.
	// Sixty-four identical cryptographic draws have negligible probability.
	if len(lengths) < 2 {
		t.Fatal("FIN length did not vary")
	}
}

func TestDataPaddingAlwaysReservesFINBudget(t *testing.T) {
	s, err := Parse([]string{"stop=64", "1=16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384,16384-16384"})
	if err != nil {
		t.Fatal(err)
	}
	base := &memoryConn{}
	c := NewConn(base, s)
	if _, err = c.Write([]byte{42}); err != nil || c.extraBytes > dataPaddingBudget {
		t.Fatal("data padding consumed FIN reserve", c.extraBytes, err)
	}
	if err = c.CloseWrite(); err != nil || c.extraBytes > MaxBudget {
		t.Fatal("close exceeded total padding budget", c.extraBytes, err)
	}
	got, err := io.ReadAll(NewConn(base, s))
	if err != nil || !bytes.Equal(got, []byte{42}) {
		t.Fatal("large valid padding prevented clean EOF", err)
	}
	// The exact boundary must remain sufficient for the largest possible FIN.
	boundary := NewConn(&memoryConn{}, s)
	boundary.extraBytes = dataPaddingBudget
	if err = boundary.CloseWrite(); err != nil || boundary.extraBytes > MaxBudget {
		t.Fatal("reserved close budget unavailable", err)
	}
}

func TestRejectInvalidOrTruncatedFIN(t *testing.T) {
	s, _ := Parse(nil)
	for _, size := range []int{0, 1, finMin - 1, finMax + 1, 65535} {
		base := &memoryConn{}
		h := header(finCommand, size)
		base.Buffer.Write(h[:])
		base.Buffer.Write(make([]byte, size))
		if _, err := NewConn(base, s).Read(make([]byte, 1)); err != errFrame {
			t.Fatal("invalid FIN length accepted", size, err)
		}
	}
	for _, n := range []int{0, 1, finMin - 1} {
		base := &memoryConn{}
		h := header(finCommand, finMin)
		base.Buffer.Write(h[:])
		base.Buffer.Write(make([]byte, n))
		if _, err := NewConn(base, s).Read(make([]byte, 1)); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("truncated FIN became clean EOF", n, err)
		}
	}
	base := &memoryConn{}
	h := header(finCommand, finMin)
	base.Buffer.Write(h[:])
	base.Buffer.Write(make([]byte, finMin))
	c := NewConn(base, s)
	c.wasteBytes = MaxBudget - finMin
	if _, err := c.Read(make([]byte, 1)); err != errFrame {
		t.Fatal("FIN bypassed receive padding budget", err)
	}
}

type failingFINConn struct {
	memoryConn
	calls, closes int
	err           error
}

func (c *failingFINConn) Write(b []byte) (int, error) {
	c.calls++
	if c.err != nil {
		return 0, c.err
	}
	return len(b) - 1, nil
}
func (c *failingFINConn) Close() error { c.closes++; return nil }

func TestFailedFINIsNotRetriedOrReportedSuccessful(t *testing.T) {
	s, _ := Parse(nil)
	for _, failure := range []error{nil, io.ErrClosedPipe} {
		base := &failingFINConn{err: failure}
		c := NewConn(base, s)
		err := c.CloseWrite()
		if err == nil || c.CloseWrite() != err || base.calls != 1 || base.closes != 1 {
			t.Fatal("partial FIN was retried or lost its error", err, base.calls, base.closes)
		}
	}
}
