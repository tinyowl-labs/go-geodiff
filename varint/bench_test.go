package varint

import (
	"math"
	"testing"
)

func BenchmarkPutVarint(b *testing.B) {
	var buf [MaxVarintLen]byte
	values := []uint32{0, 127, 128, 16383, 16384, 2097151, math.MaxUint32}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, v := range values {
			PutVarint(buf[:], v)
		}
	}
}

func BenchmarkGetVarint(b *testing.B) {
	var buf [MaxVarintLen]byte
	values := []uint32{0, 127, 128, 16383, 16384, 2097151, math.MaxUint32}
	var encoded [][]byte
	for _, v := range values {
		n := PutVarint(buf[:], v)
		chunk := make([]byte, n)
		copy(chunk, buf[:n])
		encoded = append(encoded, chunk)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, data := range encoded {
			_, _, _ = GetVarint(data, 0)
		}
	}
}
