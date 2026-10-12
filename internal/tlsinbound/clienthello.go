package tlsinbound

import (
	"encoding/binary"
	"errors"
	"io"
)

// Bound memory and time spent classifying a connection. No TLS response is sent.
const maxClientHello = 128 * 1024
const maxHelloRecords = 128

var errClientHello = errors.New("invalid or oversized TLS ClientHello")

// ReadClientHello reads the first handshake, including fragmented TLS records.
// Prefix contains the exact original records and must be replayed to the target.
// TLS negotiation, certificates and application data remain end to end.
func ReadClientHello(r io.Reader) (name string, prefix []byte, err error) {
	var handshake []byte
	for records := 0; records < maxHelloRecords; records++ {
		var header [5]byte
		if _, err = io.ReadFull(r, header[:]); err != nil {
			return "", nil, err
		}
		n := int(binary.BigEndian.Uint16(header[3:]))
		if header[0] != 22 || header[1] != 3 || header[2] > 3 || n == 0 || n > 16384 || len(prefix)+5+n > maxClientHello {
			return "", nil, errClientHello
		}
		prefix = append(prefix, header[:]...)
		start := len(prefix)
		prefix = append(prefix, make([]byte, n)...)
		if _, err = io.ReadFull(r, prefix[start:]); err != nil {
			return "", nil, err
		}
		handshake = append(handshake, prefix[start:]...)
		if len(handshake) < 4 {
			continue
		}
		size := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
		if handshake[0] != 1 || size > maxClientHello-4 {
			return "", nil, errClientHello
		}
		if len(handshake) >= size+4 {
			name, err = parseClientHello(handshake[4 : size+4])
			return name, prefix, err
		}
	}
	return "", nil, errClientHello
}

type helloReader []byte

func (r *helloReader) take(n int) ([]byte, bool) {
	if n > len(*r) {
		return nil, false
	}
	value := (*r)[:n]
	*r = (*r)[n:]
	return value, true
}
func (r *helloReader) vector(width int) ([]byte, bool) {
	header, ok := r.take(width)
	if !ok {
		return nil, false
	}
	n := int(header[0])
	if width == 2 {
		n = int(binary.BigEndian.Uint16(header))
	}
	return r.take(n)
}

func parseClientHello(data []byte) (string, error) {
	r := helloReader(data)
	if _, ok := r.take(34); !ok {
		return "", errClientHello
	}
	if session, ok := r.vector(1); !ok || len(session) > 32 {
		return "", errClientHello
	}
	if ciphers, ok := r.vector(2); !ok || len(ciphers) == 0 || len(ciphers)%2 != 0 {
		return "", errClientHello
	}
	if compression, ok := r.vector(1); !ok || len(compression) == 0 {
		return "", errClientHello
	}
	if len(r) == 0 { // Older ClientHello without extensions has no SNI.
		return "", nil
	}
	extensions, ok := r.vector(2)
	if !ok || len(r) != 0 {
		return "", errClientHello
	}
	r = helloReader(extensions)
	seen := map[uint16]bool{}
	name := ""
	for len(r) > 0 {
		kind, ok := r.take(2)
		if !ok {
			return "", errClientHello
		}
		value, ok := r.vector(2)
		id := binary.BigEndian.Uint16(kind)
		if !ok || seen[id] {
			return "", errClientHello
		}
		seen[id] = true
		if id != 0 {
			continue
		}
		sni := helloReader(value)
		list, ok := sni.vector(2)
		if !ok || len(sni) != 0 || len(list) == 0 {
			return "", errClientHello
		}
		sni = helloReader(list)
		seenTypes := map[byte]bool{}
		for len(sni) > 0 {
			typeID, ok := sni.take(1)
			if !ok {
				return "", errClientHello
			}
			host, ok := sni.vector(2)
			if !ok || len(host) == 0 || seenTypes[typeID[0]] {
				return "", errClientHello
			}
			seenTypes[typeID[0]] = true
			if typeID[0] == 0 {
				var err error
				name, err = Name(string(host))
				if err != nil {
					return "", errClientHello
				}
			}
		}
	}
	return name, nil
}
