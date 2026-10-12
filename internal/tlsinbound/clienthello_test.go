package tlsinbound

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func helloVector(value []byte) []byte {
	return append([]byte{byte(len(value) >> 8), byte(len(value))}, value...)
}
func helloBody(name string) []byte {
	body := append(make([]byte, 34), 0, 0, 2, 0x13, 1, 1, 0)
	if name == "" {
		return body
	}
	entry := append([]byte{0}, helloVector([]byte(name))...)
	ext := append([]byte{0, 0}, helloVector(helloVector(entry))...)
	return append(body, helloVector(ext)...)
}
func helloRecords(body []byte, chunk int) []byte {
	handshake := append([]byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	var records []byte
	for len(handshake) > 0 {
		n := min(chunk, len(handshake))
		records = append(records, 22, 3, 1, byte(n>>8), byte(n))
		records = append(records, handshake[:n]...)
		handshake = handshake[n:]
	}
	return records
}

// Reads can end anywhere on a TCP stream, independently of TLS record boundaries.
type oneByteReader struct{ io.Reader }

func (r oneByteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(len(p), 1)]) }

func TestReadClientHelloFragmentationAndExactReplay(t *testing.T) {
	for _, chunk := range []int{1, 2, 7, 16384} {
		wire := helloRecords(helloBody("App.Example.Test"), chunk)
		following := []byte{23, 3, 3, 0, 3, 1, 2, 3}
		r := bytes.NewReader(append(append([]byte{}, wire...), following...))
		name, prefix, err := ReadClientHello(oneByteReader{r})
		if err != nil || name != "app.example.test" || !bytes.Equal(prefix, wire) {
			t.Fatalf("fragment %d: %q %v", chunk, name, err)
		}
		replayed, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix), r))
		if err != nil || !bytes.Equal(replayed, append(wire, following...)) {
			t.Fatal("bytes were modified, lost or duplicated")
		}
	}
	// Preserve subsequent handshake bytes contained in the same TLS record.
	wire := helloRecords(helloBody("a.example.test"), 16384)
	wire = append(wire, 2, 0, 0, 1, 42)
	binary.BigEndian.PutUint16(wire[3:5], uint16(len(wire)-5))
	_, prefix, err := ReadClientHello(bytes.NewReader(wire))
	if err != nil || !bytes.Equal(prefix, wire) {
		t.Fatal("trailing bytes in the last record lost", err)
	}
}

func TestReadClientHelloRejectsMalformedAndOversized(t *testing.T) {
	valid := helloRecords(helloBody("a.example.test"), 16384)
	for i := 0; i < len(valid); i++ {
		if _, _, err := ReadClientHello(bytes.NewReader(valid[:i])); err == nil {
			t.Fatalf("truncation at %d accepted", i)
		}
	}
	for _, wire := range [][]byte{
		{23, 3, 3, 0, 4, 1, 0, 0, 0}, // application data
		{22, 3, 3, 0, 4, 2, 0, 0, 0}, // ServerHello
		{22, 3, 3, 0, 4, 1, 255, 255, 255},
		{22, 3, 3, 0x40, 1}, // oversized record
		helloRecords(helloBody("127.0.0.1"), 16384),
		helloRecords(helloBody("*.example.test"), 16384),
	} {
		if _, _, err := ReadClientHello(bytes.NewReader(wire)); err == nil {
			t.Fatal("malformed hello accepted")
		}
	}
	name, _, err := ReadClientHello(bytes.NewReader(helloRecords(helloBody(""), 16384)))
	if err != nil || name != "" {
		t.Fatal("missing SNI not classified", err)
	}
	body := helloBody("a.example.test")
	ext := body[43:]
	duplicate := append(append([]byte{}, body[:41]...), helloVector(append(append([]byte{}, ext...), ext...))...)
	if _, _, err = ReadClientHello(bytes.NewReader(helloRecords(duplicate, 16384))); err == nil {
		t.Fatal("duplicate SNI extension accepted")
	}
}

func FuzzReadClientHello(f *testing.F) {
	f.Add(helloRecords(helloBody("a.example.test"), 7))
	f.Add(helloRecords(helloBody(""), 16384))
	f.Add([]byte{22, 3, 1, 0, 0})
	f.Fuzz(func(t *testing.T, input []byte) {
		name, prefix, err := ReadClientHello(bytes.NewReader(input))
		if err == nil {
			if len(prefix) > maxClientHello || !bytes.HasPrefix(input, prefix) {
				t.Fatal("invalid replay buffer")
			}
			if name != "" {
				if _, err := Name(name); err != nil {
					t.Fatal("invalid returned name")
				}
			}
		}
	})
}
