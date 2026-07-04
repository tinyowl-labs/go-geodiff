package varint

import (
	"testing"
)

func TestPutVarint_RoundTrip(t *testing.T) {
	tests := []uint32{
		0,
		1,
		0x7f,       // max 1-byte
		0x80,       // min 2-byte
		0x3fff,     // max 2-byte = 16383
		0x4000,     // min 3-byte = 16384
		0x1fffff,   // max 3-byte = 2097151
		0x200000,   // min 4-byte
		0xfffffff,  // max 4-byte
		0x10000000, // min 5-byte
		0x7fffffff, // max 5-byte
		0x80000000, // min 9-byte
		0xffffffff, // max uint32
		42,
		1234567,
		268435455, // 2^28 - 1
		268435456, // 2^28
	}

	for _, want := range tests {
		var buf [MaxVarintLen]byte
		n := PutVarint(buf[:], want)

		got, gotN, err := GetVarint(buf[:], 0)
		if err != nil {
			t.Errorf("PutVarint+GetVarint(%d) error: %v", want, err)
			continue
		}
		if got != want {
			t.Errorf("PutVarint+GetVarint(%d) = %d, want %d", want, got, want)
		}
		if gotN != n {
			t.Errorf("PutVarint(%d) wrote %d bytes, GetVarint read %d", want, n, gotN)
		}
	}
}

func TestGetVarint_BufferBounds(t *testing.T) {
	_, _, err := GetVarint([]byte{}, 0)
	if err == nil {
		t.Error("expected error for empty buffer")
	}

	_, _, err = GetVarint([]byte{0x01}, 5)
	if err == nil {
		t.Error("expected error for offset beyond buffer")
	}
}

func TestPutVarint_KnownValues(t *testing.T) {
	tests := []struct {
		value    uint32
		expected []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{0x7f, []byte{0x7f}},              // 127: max 1-byte
		{0x80, []byte{0x81, 0x00}},        // 128: min 2-byte
		{16383, []byte{0xff, 0x7f}},       // max 2-byte: (127|0x80), 127
		{16384, []byte{0x81, 0x80, 0x00}}, // min 3-byte
	}

	for _, tt := range tests {
		var buf [MaxVarintLen]byte
		n := PutVarint(buf[:], tt.value)
		if n != len(tt.expected) {
			t.Errorf("PutVarint(%d) wrote %d bytes, want %d (buf: % x)", tt.value, n, len(tt.expected), buf[:n])
			continue
		}
		for i := 0; i < n; i++ {
			if buf[i] != tt.expected[i] {
				t.Errorf("PutVarint(%d)[%d] = 0x%02x, want 0x%02x (full buf: % x)", tt.value, i, buf[i], tt.expected[i], buf[:n])
			}
		}
	}
}
