package audioquality

import "testing"

func BenchmarkChannelDepth(b *testing.B) {
	for _, padded := range []bool{false, true} {
		name := "native24"
		if padded {
			name = "padded16in24"
		}
		b.Run(name, func(b *testing.B) {
			samples := noise(32768, 53)
			for i := range samples {
				samples[i] <<= 8
				if !padded {
					samples[i] |= int32(i % 251)
				}
			}
			b.SetBytes(int64(len(samples) * 4))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				depth := newDepth(24)
				for _, sample := range samples {
					depth.push(sample)
				}
				want := 24
				if padded {
					want = 16
				}
				if depth.depth(24) != want {
					b.Fatal("invalid measured depth")
				}
			}
		})
	}
}
