// SPDX-License-Identifier: MPL-2.0
// FlacCompagnon-derived measurements; see SOURCE.md for attribution and changes.
package audioquality

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
)

var ErrUnsupported = errors.New("此格式暂不支持音源检测，当前支持 FLAC、WAV 和 AIFF 的整数 PCM")

type Result struct {
	Status             string  `json:"status"`
	Message            string  `json:"message"`
	SampleRate         int     `json:"sample_rate,omitempty"`
	DeclaredBits       int     `json:"declared_bits,omitempty"`
	EffectiveBits      int     `json:"effective_bits,omitempty"`
	StoredBits         int     `json:"stored_bits,omitempty"`
	BitDepthMethod     string  `json:"bit_depth_method,omitempty"`
	Upscaled           bool    `json:"upscaled"`
	PossibleUpsampling bool    `json:"possible_upsampling"`
	CutoffHz           float64 `json:"cutoff_hz,omitempty"`
	BandwidthHz        float64 `json:"bandwidth_hz,omitempty"`
	SpectralWindows    int     `json:"spectral_windows"`
	Samples            uint64  `json:"samples"`
}

type streamInfo struct {
	rate, channels, bits int
	frames               uint64
}
type pcmStream struct {
	info streamInfo
	read func(func([]int32) error) error
}

type analyzer struct {
	info                               streamInfo
	depth                              []channelDepth
	blocks                             [][]float64
	energy                             []float64
	frame, block, nextWindow, interval uint64
	windowCount                        int
	spectrum                           spectrum
	peak                               int64
}

func newAnalyzer(info streamInfo) *analyzer {
	a := &analyzer{info: info, interval: max(1, ((info.frames+2047)/2048+239)/240), blocks: make([][]float64, info.channels), energy: make([]float64, info.channels)}
	for c := 0; c < info.channels; c++ {
		a.depth = append(a.depth, newDepth(info.bits))
		a.blocks[c] = make([]float64, 2048)
	}
	return a
}

func (a *analyzer) push(samples []int32) error {
	if len(samples) != a.info.channels {
		return errors.New("音频声道数量变化")
	}
	pos := int(a.frame % 2048)
	selected := a.block >= a.nextWindow && a.windowCount < 240
	for c, v := range samples {
		a.depth[c].push(v)
		a.peak = max(a.peak, int64(v), -int64(v))
		if selected {
			f := float64(v) / math.Ldexp(1, a.info.bits-1)
			a.blocks[c][pos] = f
			a.energy[c] += f * f
		}
	}
	a.frame++
	if pos == 2047 {
		if selected {
			// Analyze the strongest channel so anti-phase stereo cannot cancel
			// into silence. Integer precision always examines every channel.
			channel := 0
			for c := range a.energy {
				if a.energy[c] > a.energy[channel] {
					channel = c
				}
			}
			a.spectrum.push(a.blocks[channel])
			clear(a.energy)
			a.windowCount++
			a.nextWindow = a.block + a.interval
		}
		a.block++
	}
	return nil
}

func (a *analyzer) finish() Result {
	r := Result{Status: "analyzed", SampleRate: a.info.rate, DeclaredBits: a.info.bits, Samples: a.frame, SpectralWindows: a.spectrum.frames, BitDepthMethod: "stored"}
	var mask uint32
	for _, c := range a.depth {
		mask |= c.mask
		r.EffectiveBits = max(r.EffectiveBits, c.depth(a.info.bits))
	}
	r.StoredBits = effectiveBits(mask, a.info.bits)
	if r.EffectiveBits < r.StoredBits {
		r.BitDepthMethod = "grid"
	}
	r.Upscaled = r.EffectiveBits < r.DeclaredBits && a.peak != 0
	r.PossibleUpsampling, r.BandwidthHz = a.spectrum.upsampling(a.info.rate)
	r.CutoffHz = math.Round(a.spectrum.cutoff(a.info.rate))
	r.BandwidthHz = math.Round(r.BandwidthHz)
	switch {
	case a.frame == 0:
		r.Status = "inconclusive"
		r.Message = "音频没有可分析的样本"
	case a.peak == 0:
		r.Status = "inconclusive"
		r.Message = "数字静音，无法判断音源质量"
	case r.Upscaled && r.PossibleUpsampling:
		r.Message = "位深利用不足，并存在疑似升频特征"
	case r.Upscaled:
		r.Message = "样本使用的有效位深低于文件标称值"
	case r.PossibleUpsampling:
		r.Message = "高频带宽受限，疑似升频；正常低通处理也可能出现此特征"
	case a.info.rate > 48000 && r.SpectralWindows < 12:
		r.Status = "inconclusive"
		r.Message = "有效分析片段不足，无法判断升频"
	default:
		r.Message = "已完成的检测未发现异常；不代表已验证原始母带规格"
	}
	return r
}

// Analyze never resamples, dithers or modifies audio. Integer bit-depth is
// measured across the complete stream; up to 240 spectral windows span it.
func Analyze(ctx context.Context, r io.ReadSeeker, format string) (result Result, err error) {
	// Uploaded media is untrusted; a decoder panic must not stop the worker or
	// publish a partially measured result as successful.
	defer func() {
		if recover() != nil {
			result = Result{}
			err = errors.New("音频解码失败：文件结构无效")
		}
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	stream, err := openPCM(contextReader{ctx: ctx, ReadSeeker: r}, format)
	if err != nil {
		return Result{}, err
	}
	i := stream.info
	if i.channels < 1 || i.channels > 8 || i.bits < 1 || i.bits > 32 || i.rate < 1 || i.rate > 1536000 {
		return Result{}, errors.New("音频参数不受支持")
	}
	if format == "FLAC" && i.frames == 0 {
		// STREAMINFO may omit the length. Count in a bounded-memory first pass
		// so spectral windows can still be distributed across the entire file.
		var frames uint64
		if err := stream.read(func([]int32) error { frames++; return ctx.Err() }); err != nil {
			return Result{}, err
		}
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return Result{}, err
		}
		stream, err = openPCM(contextReader{ctx: ctx, ReadSeeker: r}, format)
		if err != nil {
			return Result{}, err
		}
		i.frames = frames
	}
	a := newAnalyzer(i)
	err = stream.read(func(samples []int32) error {
		if a.frame%2048 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		return a.push(samples)
	})
	if err != nil {
		return Result{}, err
	}
	if i.frames != 0 && a.frame != i.frames {
		return Result{}, fmt.Errorf("音频不完整：预期 %d 个样本，读取 %d 个", i.frames, a.frame)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return a.finish(), nil
}

type contextReader struct {
	io.ReadSeeker
	ctx context.Context
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReadSeeker.Read(p)
}
func (r contextReader) Seek(off int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReadSeeker.Seek(off, whence)
}
