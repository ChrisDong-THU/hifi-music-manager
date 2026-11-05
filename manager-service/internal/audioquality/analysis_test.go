package audioquality

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"testing"
)

func wave(samples [][]int32, rate, bits int) []byte {
	channels := len(samples)
	size := len(samples[0]) * channels * bits / 8
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+size))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(channels))
	binary.Write(&b, binary.LittleEndian, uint32(rate))
	binary.Write(&b, binary.LittleEndian, uint32(rate*channels*bits/8))
	binary.Write(&b, binary.LittleEndian, uint16(channels*bits/8))
	binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(size))
	for i := range samples[0] {
		for _, channel := range samples {
			v := channel[i]
			if bits == 8 {
				v += 128
			}
			for j := 0; j < bits/8; j++ {
				b.WriteByte(byte(v >> uint(8*j)))
			}
		}
	}
	return b.Bytes()
}

func analyzeWAV(t *testing.T, samples [][]int32, rate, bits int) Result {
	t.Helper()
	r, err := Analyze(context.Background(), bytes.NewReader(wave(samples, rate, bits)), "WAV")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func noise(n int, seed int64) []int32 {
	r := rand.New(rand.NewSource(seed))
	out := make([]int32, n)
	for i := range out {
		out[i] = r.Int31n(60000) - 30000
	}
	return out
}

func TestIntegerPrecisionAcrossAllChannelsAndWholeTrack(t *testing.T) {
	x := noise(44100, 1)
	for i := range x {
		x[i] <<= 8
	}
	r := analyzeWAV(t, [][]int32{x}, 44100, 24)
	if !r.Upscaled || r.EffectiveBits != 16 || r.StoredBits != 16 || r.BitDepthMethod != "stored" {
		t.Fatalf("padded PCM: %+v", r)
	}
	y := append([]int32(nil), x...)
	y[len(y)-1] |= 37 // Beyond every allowed low-residual quantization grid.
	r = analyzeWAV(t, [][]int32{x, y}, 44100, 24)
	if r.Upscaled || r.EffectiveBits != 24 {
		t.Fatalf("last sample / second channel was ignored: %+v", r)
	}
	for i := range x {
		x[i] += int32(i%21) - 10
	}
	r = analyzeWAV(t, [][]int32{x}, 44100, 24)
	if !r.Upscaled || r.EffectiveBits != 16 || r.StoredBits != 24 || r.BitDepthMethod != "grid" {
		t.Fatalf("bounded quantization residual: %+v", r)
	}
}

func TestMDCTMatchesIndependentDirectDefinition(t *testing.T) {
	frame := make([]float64, 2048)
	r := rand.New(rand.NewSource(19))
	for i := range frame {
		frame[i] = r.Float64()*2 - 1
	}
	got := mdct(frame)
	n := len(got)
	for _, k := range []int{0, 1, 63, 511, 1023} {
		want := 0.0
		for j, v := range frame {
			want += v * math.Sin(math.Pi*(float64(j)+0.5)/float64(2*n)) * math.Cos(math.Pi/float64(n)*(float64(j)+0.5+float64(n)/2)*(float64(k)+0.5))
		}
		if math.Abs(got[k]-want) > 1e-8 {
			t.Fatalf("coefficient %d: %.12f != %.12f", k, got[k], want)
		}
	}
}

func TestBandwidthHeuristicAndSilence(t *testing.T) {
	const rate = 96000
	x := make([]int32, rate)
	for i := range x {
		v := 0.0
		for k := 1; k < 24; k++ {
			v += math.Sin(2*math.Pi*float64(k*700)*float64(i)/rate+float64(k)) / 24
		}
		x[i] = int32(v * 4000000)
	}
	r := analyzeWAV(t, [][]int32{x}, rate, 24)
	if !r.PossibleUpsampling || r.SpectralWindows < 12 || r.BandwidthHz > 22000 {
		t.Fatalf("band-limited high-rate PCM: %+v", r)
	}
	y := noise(rate, 2)
	for i := range y {
		y[i] = y[i]*127 + int32(i%3)
	}
	r = analyzeWAV(t, [][]int32{y}, rate, 24)
	if r.PossibleUpsampling || r.Upscaled {
		t.Fatalf("broadband native precision: %+v", r)
	}
	// A 479-window file must sample beyond its first 240 windows. Its second
	// half has full-band audio; a prefix-only scan would incorrectly flag it.
	late := make([]int32, 479*2048)
	for i := range late {
		if i < len(late)/2 {
			late[i] = x[i%len(x)]
		} else {
			late[i] = y[i%len(y)]
		}
	}
	r = analyzeWAV(t, [][]int32{late}, rate, 24)
	if r.PossibleUpsampling {
		t.Fatalf("spectral sampling did not cover the whole track: %+v", r)
	}
	r = analyzeWAV(t, [][]int32{make([]int32, rate)}, rate, 24)
	if r.Status != "inconclusive" || r.PossibleUpsampling || r.Upscaled {
		t.Fatalf("silence cannot establish provenance: %+v", r)
	}
	r = analyzeWAV(t, [][]int32{x[:2048]}, rate, 24)
	if r.Status != "inconclusive" || r.PossibleUpsampling {
		t.Fatalf("too little evidence: %+v", r)
	}
}

func TestDecodingAndCancellationErrorsNeverPassAsQuality(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Analyze(ctx, bytes.NewReader(nil), "FLAC"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), bytes.NewReader(nil), "MP3"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	data := wave([][]int32{noise(4096, 3)}, 44100, 16)
	if _, err := Analyze(context.Background(), bytes.NewReader(data[:len(data)-2]), "WAV"); err == nil {
		t.Fatal("truncated audio passed")
	}
	data[20] = 3 // IEEE float is deliberately not interpreted as integer PCM.
	if _, err := Analyze(context.Background(), bytes.NewReader(data), "WAV"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestUnsigned8BitAndAIFF(t *testing.T) {
	x := make([]int32, 4096)
	for i := range x {
		x[i] = int32(i%255) - 127
	}
	r := analyzeWAV(t, [][]int32{x}, 44100, 8)
	if r.EffectiveBits != 8 {
		t.Fatal(r)
	}
	var b bytes.Buffer
	b.WriteString("FORM")
	binary.Write(&b, binary.BigEndian, uint32(46+len(x)*2))
	b.WriteString("AIFFCOMM")
	binary.Write(&b, binary.BigEndian, uint32(18))
	binary.Write(&b, binary.BigEndian, uint16(1))
	binary.Write(&b, binary.BigEndian, uint32(len(x)))
	binary.Write(&b, binary.BigEndian, uint16(16))
	b.Write([]byte{0x40, 0x0e, 0xac, 0x44, 0, 0, 0, 0, 0, 0})
	b.WriteString("SSND")
	binary.Write(&b, binary.BigEndian, uint32(8+len(x)*2))
	b.Write(make([]byte, 8))
	for _, v := range x {
		binary.Write(&b, binary.BigEndian, int16(v))
	}
	r, err := Analyze(context.Background(), bytes.NewReader(b.Bytes()), "AIFF")
	if err != nil || r.SampleRate != 44100 || r.Samples != uint64(len(x)) {
		t.Fatal(r, err)
	}
}
