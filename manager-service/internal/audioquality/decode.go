package audioquality

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"github.com/mewkiz/flac"
)

func openPCM(r io.ReadSeeker, format string) (pcmStream, error) {
	switch format {
	case "FLAC":
		return openFLAC(r)
	case "WAV", "AIFF", "AIF":
		return openRawPCM(r, format)
	default:
		return pcmStream{}, ErrUnsupported
	}
}

func openFLAC(r io.ReadSeeker) (pcmStream, error) {
	s, err := flac.New(r)
	if err != nil {
		return pcmStream{}, err
	}
	info := streamInfo{int(s.Info.SampleRate), int(s.Info.NChannels), int(s.Info.BitsPerSample), s.Info.NSamples}
	return pcmStream{info: info, read: func(push func([]int32) error) error {
		values := make([]int32, info.channels)
		for {
			frame, err := s.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if frame.BitsPerSample == 0 {
				frame.BitsPerSample = s.Info.BitsPerSample
			}
			if frame.SampleRate == 0 {
				frame.SampleRate = s.Info.SampleRate
			}
			if int(frame.BitsPerSample) != info.bits || int(frame.SampleRate) != info.rate {
				return errors.New("FLAC 帧参数与文件头不一致")
			}
			if err = frame.Parse(); err != nil {
				return err
			}
			if len(frame.Subframes) != info.channels || int(frame.BitsPerSample) != info.bits || int(frame.SampleRate) != info.rate {
				return errors.New("FLAC 帧参数与文件头不一致")
			}
			for c := range frame.Subframes {
				if len(frame.Subframes[c].Samples) != int(frame.BlockSize) {
					return errors.New("FLAC 帧样本数量不一致")
				}
			}
			for i := 0; i < int(frame.BlockSize); i++ {
				for c := range values {
					values[c] = frame.Subframes[c].Samples[i]
				}
				if err := push(values); err != nil {
					return err
				}
			}
		}
	}}, nil
}

// Read bounded RIFF/FORM chunks, then stream complete interleaved PCM frames.
// Container padding is removed before measuring valid integer precision.
func openRawPCM(r io.ReadSeeker, format string) (pcmStream, error) {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return pcmStream{}, err
	}
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return pcmStream{}, err
	}
	h := make([]byte, 12)
	if _, err = io.ReadFull(r, h); err != nil {
		return pcmStream{}, err
	}
	wave := format == "WAV"
	var order binary.ByteOrder = binary.BigEndian
	if wave {
		order = binary.LittleEndian
		if string(h[:4]) != "RIFF" || string(h[8:]) != "WAVE" {
			return pcmStream{}, errors.New("无效 WAV 文件")
		}
	} else if string(h[:4]) != "FORM" || (string(h[8:]) != "AIFF" && string(h[8:]) != "AIFC") {
		return pcmStream{}, errors.New("无效 AIFF 文件")
	}
	containerEnd := int64(order.Uint32(h[4:8])) + 8
	if containerEnd > end {
		return pcmStream{}, io.ErrUnexpectedEOF
	}
	end = containerEnd
	info := streamInfo{}
	sampleOrder := order
	containerBits, align := 0, 0
	dataStart, dataSize := int64(0), int64(0)
	unsigned := false
	for off, count := int64(12), 0; off <= end-8 && count < 4096; count++ {
		if _, err = r.Seek(off, io.SeekStart); err != nil {
			return pcmStream{}, err
		}
		chunk := make([]byte, 8)
		if _, err = io.ReadFull(r, chunk); err != nil {
			return pcmStream{}, err
		}
		n := int64(order.Uint32(chunk[4:]))
		if n > end-off-8 {
			return pcmStream{}, io.ErrUnexpectedEOF
		}
		kind := string(chunk[:4])
		switch {
		case wave && kind == "fmt ":
			if n < 16 {
				return pcmStream{}, errors.New("无效 WAV 格式块")
			}
			b := make([]byte, min(n, 40))
			if _, err = io.ReadFull(r, b); err != nil {
				return pcmStream{}, err
			}
			code := order.Uint16(b[:2])
			info.channels = int(order.Uint16(b[2:4]))
			info.rate = int(order.Uint32(b[4:8]))
			align = int(order.Uint16(b[12:14]))
			containerBits = int(order.Uint16(b[14:16]))
			info.bits = containerBits
			if code == 0xfffe && len(b) >= 40 && order.Uint16(b[16:18]) >= 22 {
				if !bytes.Equal(b[24:40], []byte{1, 0, 0, 0, 0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113}) {
					return pcmStream{}, ErrUnsupported
				}
				code = 1
				if valid := int(order.Uint16(b[18:20])); valid > 0 {
					info.bits = valid
				}
			}
			if code != 1 {
				return pcmStream{}, ErrUnsupported
			}
			unsigned = containerBits == 8
		case !wave && kind == "COMM":
			if n < 18 {
				return pcmStream{}, errors.New("无效 AIFF 格式块")
			}
			b := make([]byte, min(n, 22))
			if _, err = io.ReadFull(r, b); err != nil {
				return pcmStream{}, err
			}
			info.channels = int(order.Uint16(b[:2]))
			info.frames = uint64(order.Uint32(b[2:6]))
			info.bits = int(order.Uint16(b[6:8]))
			containerBits = ((info.bits + 7) / 8) * 8
			align = info.channels * containerBits / 8
			exp := order.Uint16(b[8:10])
			rate := math.Ldexp(float64(order.Uint64(b[10:18])), int(exp)-16383-63)
			if rate < 1 || rate > 1536000 || math.IsNaN(rate) || math.IsInf(rate, 0) {
				return pcmStream{}, errors.New("无效采样率")
			}
			info.rate = int(math.Round(rate))
			if string(h[8:]) == "AIFC" {
				if len(b) < 22 {
					return pcmStream{}, ErrUnsupported
				}
				switch string(b[18:22]) {
				case "NONE", "twos":
				case "sowt":
					sampleOrder = binary.LittleEndian
				default:
					return pcmStream{}, ErrUnsupported
				}
			}
		case wave && kind == "data":
			dataStart, dataSize = off+8, n
		case !wave && kind == "SSND":
			if n < 8 {
				return pcmStream{}, errors.New("无效 AIFF 音频块")
			}
			b := make([]byte, 8)
			if _, err = io.ReadFull(r, b); err != nil {
				return pcmStream{}, err
			}
			offset := int64(binary.BigEndian.Uint32(b[:4]))
			if offset > n-8 {
				return pcmStream{}, io.ErrUnexpectedEOF
			}
			dataStart, dataSize = off+16+offset, n-8-offset
		}
		if dataStart != 0 && info.bits != 0 {
			break
		}
		off += 8 + n + n%2
	}
	if dataStart == 0 || info.channels < 1 || info.channels > 8 || info.bits < 1 || info.bits > 32 || containerBits > 32 || containerBits < info.bits || containerBits%8 != 0 || align != info.channels*containerBits/8 || dataSize%int64(max(align, 1)) != 0 {
		return pcmStream{}, ErrUnsupported
	}
	frames := uint64(dataSize / int64(align))
	if !wave && frames != info.frames {
		return pcmStream{}, errors.New("AIFF 样本数量与文件头不一致")
	}
	info.frames = frames
	return pcmStream{info: info, read: func(push func([]int32) error) error {
		if _, err := r.Seek(dataStart, io.SeekStart); err != nil {
			return err
		}
		values := make([]int32, info.channels)
		block := make([]byte, align*2048)
		remaining := dataSize
		for remaining > 0 {
			n := int(min(int64(len(block)), remaining))
			if _, err := io.ReadFull(r, block[:n]); err != nil {
				return err
			}
			for offset := 0; offset < n; offset += align {
				for c := range values {
					b := block[offset+c*containerBits/8 : offset+(c+1)*containerBits/8]
					var v uint32
					if sampleOrder == binary.LittleEndian {
						for j := len(b) - 1; j >= 0; j-- {
							v = v<<8 | uint32(b[j])
						}
					} else {
						for _, x := range b {
							v = v<<8 | uint32(x)
						}
					}
					if unsigned {
						values[c] = int32(v) - 128
					} else {
						values[c] = (int32(v) << uint(32-containerBits)) >> uint(32-containerBits)
					}
					values[c] >>= uint(containerBits - info.bits)
				}
				if err := push(values); err != nil {
					return err
				}
			}
			remaining -= int64(n)
		}
		return nil
	}}, nil
}
