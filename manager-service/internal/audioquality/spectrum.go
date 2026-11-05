// SPDX-License-Identifier: MPL-2.0
// Adapted from FlacCompagnon analysis/{mdct,analyzer,spectrum,detections}.rs,
// commit 7a382761a2054e07d3572736101e64560ef09057. FlacCompagnon contributors.
package audioquality

import (
	"math"
	"math/cmplx"
)

// A radix-2 FFT evaluates the same sine-window MDCT definition as upstream
// without the O(N²) cosine matrix. Verified against the direct definition.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		root := cmplx.Rect(1, -2*math.Pi/float64(size))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for j := 0; j < size/2; j++ {
				a, b := x[start+j], w*x[start+j+size/2]
				x[start+j], x[start+j+size/2] = a+b, a-b
				w *= root
			}
		}
	}
}

func mdct(frame []float64) []float64 {
	n := len(frame) / 2
	x := make([]complex128, len(frame))
	for t, v := range frame {
		angle := math.Pi * (float64(t) + 0.5) / float64(2*n)
		x[t] = complex(v*math.Sin(angle), 0) * cmplx.Rect(1, -math.Pi*float64(t)/float64(2*n))
	}
	fft(x)
	out := make([]float64, n)
	for k := range out {
		phase := -math.Pi / float64(n) * (0.5 + float64(n)/2) * (float64(k) + 0.5)
		out[k] = real(x[k] * cmplx.Rect(1, phase))
	}
	return out
}

type spectrum struct {
	frames, dead         int
	cutoffSum, deadDBSum float64
	power                []float64
}

func (s *spectrum) push(frame []float64) {
	// Silent windows do not establish the bandwidth of an audio programme.
	var energy float64
	for _, v := range frame {
		energy += v * v
	}
	if energy < 1e-14*float64(len(frame)) {
		return
	}
	coeff := mdct(frame)
	peak := 0.0
	for _, v := range coeff {
		peak = max(peak, math.Abs(v))
	}
	if peak < 1e-7 {
		return
	}
	s.frames++
	cutoff := 0
	for k := len(coeff) - 1; k >= 0; k-- {
		if math.Abs(coeff[k]) > peak*1e-4 {
			cutoff = k
			break
		}
	}
	if cutoff < int(0.92*float64(len(coeff))) && cutoff+8 < len(coeff) {
		var sum float64
		for _, v := range coeff[cutoff+8:] {
			sum += 20 * math.Log10(max(math.Abs(v)/peak, 1e-12))
		}
		s.dead++
		s.deadDBSum += sum / float64(len(coeff)-cutoff-8)
		s.cutoffSum += float64(cutoff) / float64(len(coeff))
	}
	x := make([]complex128, len(frame))
	for i, v := range frame {
		x[i] = complex(v*(0.5-0.5*math.Cos(2*math.Pi*float64(i)/float64(len(frame)-1))), 0)
	}
	fft(x)
	if s.power == nil {
		s.power = make([]float64, len(frame)/2+1)
	}
	for k := range s.power {
		s.power[k] += real(x[k])*real(x[k]) + imag(x[k])*imag(x[k])
	}
}

func (s spectrum) cutoff(rate int) float64 {
	if s.frames == 0 {
		return 0
	}
	peak := 0.0
	for _, v := range s.power {
		peak = max(peak, v)
	}
	levels := make([]float64, len(s.power))
	for k, v := range s.power {
		levels[k] = 10 * math.Log10(max(v/peak, 1e-24))
	}
	for k := len(levels) - 1; k >= 0; k-- {
		var sum float64
		lo, hi := max(0, k-5), min(len(levels), k+6)
		for _, v := range levels[lo:hi] {
			sum += v
		}
		if sum/float64(hi-lo) > -90 {
			return float64(k) * float64(rate) / float64(2*(len(levels)-1))
		}
	}
	return 0
}

func (s spectrum) upsampling(rate int) (bool, float64) {
	if s.dead == 0 {
		return false, 0
	}
	ratio := s.cutoffSum / float64(s.dead)
	cutoff := ratio * float64(rate) / 2
	return rate > 48000 && s.frames >= 12 && float64(s.dead)/float64(s.frames) >= 0.70 && ratio < 0.90 && s.deadDBSum/float64(s.dead) < -75 && cutoff <= 30000, cutoff
}
