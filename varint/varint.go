// Package varint implements SQLite's variable-length integer encoding
// as used in the session extension's changeset format.
//
// This is a direct port of changesetgetvarint.h and changesetputvarint.h
// from the geodiff C++ library (MIT, Lutra Consulting).
//
// Encoding (putVarint64 in C++):
//   - Values 0..0x7f: 1 byte (value as-is)
//   - Values 0x80..0x3fff: 2 bytes
//   - Values >= 0x4000: variable length
//     Build 7-bit chunks from low to high, set high bit on all,
//     clear high bit on last (which is the first chunk, LSB),
//     then reverse into output.
//     If value >= 2^56: 9 bytes with special last byte.
package varint

import "fmt"

// MaxVarintLen is the maximum number of bytes a varint can occupy (9).
const MaxVarintLen = 9

// ErrVarintTooLong is returned when a varint exceeds the maximum length.
var ErrVarintTooLong = fmt.Errorf("varint too long (max %d bytes)", MaxVarintLen)

// GetVarint reads a 32-bit variable-length integer from buf starting at
// offset p. Returns the value, number of bytes consumed, and any error.
//
// If the encoded value exceeds uint32 range, returns 0xffffffff.
func GetVarint(buf []byte, p int) (value uint32, n int, err error) {
	if p >= len(buf) {
		return 0, 0, fmt.Errorf("GetVarint: offset %d beyond buffer length %d", p, len(buf))
	}

	buf = buf[p:]

	// 1-byte case: values 0-127
	if buf[0] < 0x80 {
		return uint32(buf[0]), 1, nil
	}

	// 2-byte case: values 128-16383
	if len(buf) >= 2 && buf[1] < 0x80 {
		a := uint32(buf[0] & 0x7f)
		b := uint32(buf[1])
		return (a << 7) | b, 2, nil
	}

	// 3-byte case: values 16384-2097151
	if len(buf) >= 3 {
		a := (uint32(buf[0]) << 14) | uint32(buf[2])
		if a&0x80 == 0 {
			a &= (0x7f << 14) | 0x7f
			b := (uint32(buf[1]) & 0x7f) << 7
			return a | b, 3, nil
		}
	}

	// Fall back to full 64-bit varint decode, then clamp to uint32
	v64, n64, err := getVarint64(buf)
	if err != nil {
		return 0, 0, err
	}
	if v64 > 0xffffffff {
		return 0xffffffff, n64, nil
	}
	return uint32(v64), n64, nil
}

// getVarint64 reads a full 64-bit variable-length integer.
func getVarint64(buf []byte) (value uint64, n int, err error) {
	// 1-byte
	if len(buf) == 0 {
		return 0, 0, fmt.Errorf("getVarint64: empty buffer")
	}
	if buf[0] < 0x80 {
		return uint64(buf[0]), 1, nil
	}

	// 2-8 byte: each byte has 7 bits of data + high bit set
	// 9th byte: full 8 bits
	// First check if we have a 9-byte varint (all first 8 have high bit set)
	if len(buf) >= 9 {
		allHigh := true
		for i := 0; i < 8; i++ {
			if buf[i]&0x80 == 0 {
				allHigh = false
				break
			}
		}
		if allHigh {
			// 9-byte encoding
			value = uint64(buf[8])
			for i := 7; i >= 0; i-- {
				value |= uint64(buf[i]&0x7f) << (7*(7-uint(i)) + 8)
			}
			return value, 9, nil
		}
	}

	// Standard 2-8 byte varint
	// Read 7-bit chunks until we find one without high bit set
	value = 0
	for n = 0; n < 8 && n < len(buf); n++ {
		b := buf[n]
		value = (value << 7) + uint64(b&0x7f)
		if b&0x80 == 0 {
			n++
			return value, n, nil
		}
	}

	// If we ran out of buffer or all 8 had high bit set but no 9th
	if n >= len(buf) {
		return 0, 0, fmt.Errorf("getVarint64: unexpected end of buffer")
	}
	return 0, 0, ErrVarintTooLong
}

// PutVarint encodes value as a variable-length integer into buf.
// Returns the number of bytes written (1-9).
//
// Panics if buf is too short (must be at least MaxVarintLen bytes).
func PutVarint(buf []byte, value uint32) int {
	return putVarint64(buf, uint64(value))
}

// putVarint64 encodes a uint64 value. This is the same as the C++ putVarint64.
func putVarint64(p []byte, v uint64) int {
	// 1-byte case
	if v <= 0x7f {
		p[0] = byte(v)
		return 1
	}

	// 2-byte case
	if v <= 0x3fff {
		p[0] = byte(((v >> 7) & 0x7f) | 0x80)
		p[1] = byte(v & 0x7f)
		return 2
	}

	// 9-byte case: value >= 2^56
	if v&(0xff000000_00000000) != 0 {
		p[8] = byte(v)
		v >>= 8
		for i := 7; i >= 0; i-- {
			p[i] = byte((v & 0x7f) | 0x80)
			v >>= 7
		}
		return 9
	}

	// General case: build 7-bit chunks, reverse
	var buf [10]byte
	n := 0
	for {
		buf[n] = byte((v & 0x7f) | 0x80)
		n++
		v >>= 7
		if v == 0 {
			break
		}
	}
	buf[0] &= 0x7f // clear high bit on the last (least significant) chunk

	// Reverse into output
	for i, j := 0, n-1; j >= 0; i, j = i+1, j-1 {
		p[i] = buf[j]
	}
	return n
}
