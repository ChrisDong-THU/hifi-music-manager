package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
)

// AudioInfo describes the encoded stream, never the browser's decoded output
// or values guessed from tags/filenames. Zero means unavailable/not applicable.
type AudioInfo struct {
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	BitDepth   int    `json:"bit_depth,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

// Only bounded headers are read. No decoder, subprocess or network is needed.
// An unsupported/truncated header must not prevent an otherwise valid import.
func readAudioInfo(f *os.File, format string) AudioInfo {
	st, err := f.Stat()
	if err != nil {
		return AudioInfo{}
	}
	read := func(off int64, n int) []byte {
		if off < 0 || n < 0 || n > 65536 || off > st.Size()-int64(n) {
			return nil
		}
		b := make([]byte, n)
		if _, err := f.ReadAt(b, off); err != nil {
			return nil
		}
		return b
	}
	var info AudioInfo
	switch format {
	case "FLAC":
		if b := read(0, 42); b != nil && string(b[:4]) == "fLaC" && b[4]&127 == 0 && bytes.Equal(b[5:8], []byte{0, 0, 34}) {
			v := binary.BigEndian.Uint64(b[18:26])
			info = AudioInfo{SampleRate: int(v >> 44), BitDepth: int(v>>36&31) + 1, Channels: int(v>>41&7) + 1}
		}
	case "MP3":
		off := int64(0)
		if b := read(0, 10); b != nil && string(b[:3]) == "ID3" {
			for _, v := range b[6:10] {
				if v&128 != 0 {
					return AudioInfo{}
				}
			}
			off = 10 + int64(b[6])<<21 + int64(b[7])<<14 + int64(b[8])<<7 + int64(b[9])
			if b[3] == 4 && b[5]&16 != 0 {
				off += 10
			}
		}
		if b := read(off, 4); b != nil && b[0] == 255 && b[1]&224 == 224 {
			version, index := b[1]>>3&3, b[2]>>2&3
			if version != 1 && index != 3 && b[1]>>1&3 != 0 {
				rate := []int{44100, 48000, 32000}[index]
				if version == 2 {
					rate /= 2
				}
				if version == 0 {
					rate /= 4
				}
				channels := 2
				if b[3]>>6 == 3 {
					channels = 1
				}
				info = AudioInfo{SampleRate: rate, Channels: channels}
			}
		}
	case "WAV", "AIFF", "AIF":
		h := read(0, 12)
		if h == nil {
			break
		}
		wave := format == "WAV"
		var order binary.ByteOrder = binary.BigEndian
		if wave {
			if string(h[:4]) != "RIFF" || string(h[8:]) != "WAVE" {
				break
			}
			order = binary.LittleEndian
		} else if string(h[:4]) != "FORM" || (string(h[8:]) != "AIFF" && string(h[8:]) != "AIFC") {
			break
		}
		end := min(st.Size(), int64(order.Uint32(h[4:8]))+8)
		for off, count := int64(12), 0; count < 4096 && off <= end-8; count++ {
			h := read(off, 8)
			if h == nil {
				break
			}
			n := int64(order.Uint32(h[4:]))
			if n > end-off-8 {
				break
			}
			if wave && string(h[:4]) == "fmt " && n >= 16 {
				b := read(off+8, int(min(n, 40)))
				if b == nil {
					break
				}
				kind := order.Uint16(b[:2])
				info.SampleRate, info.Channels = int(order.Uint32(b[4:8])), int(order.Uint16(b[2:4]))
				if kind == 1 || kind == 3 {
					info.BitDepth = int(order.Uint16(b[14:16]))
				}
				if kind == 0xfffe && len(b) >= 40 && order.Uint16(b[16:18]) >= 22 {
					// WAVEFORMATEXTENSIBLE: valid precision may be smaller than the container.
					guid := b[24:40]
					if (guid[0] == 1 || guid[0] == 3) && bytes.Equal(guid[1:], []byte{0, 0, 0, 0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113}) {
						info.BitDepth = int(order.Uint16(b[14:16]))
						if valid := int(order.Uint16(b[18:20])); valid > 0 && valid <= info.BitDepth {
							info.BitDepth = valid
						}
					}
				}
				break
			}
			if !wave && string(h[:4]) == "COMM" && n >= 18 {
				b := read(off+8, int(min(n, 22)))
				if b == nil {
					break
				}
				info.Channels = int(order.Uint16(b[:2]))
				exp := order.Uint16(b[8:10])
				if exp > 0 && exp < 0x7fff {
					rate := math.Ldexp(float64(order.Uint64(b[10:18])), int(exp)-16383-63)
					if rate > 0 && rate <= 1536000 {
						info.SampleRate = int(math.Round(rate))
					}
				}
				if string(read(8, 4)) == "AIFF" || (len(b) >= 22 && (string(b[18:22]) == "NONE" || string(b[18:22]) == "sowt" || string(b[18:22]) == "twos" || string(b[18:22]) == "fl32" || string(b[18:22]) == "fl64")) {
					info.BitDepth = int(order.Uint16(b[6:8]))
				}
				break
			}
			off += 8 + n + n%2
		}
	case "OGG", "OPUS":
		// Identification is the first packet in the first Ogg page.
		h := read(0, 27)
		if h == nil || string(h[:4]) != "OggS" || h[4] != 0 || h[5]&2 == 0 {
			break
		}
		laces := read(27, int(h[26]))
		n := 0
		complete := false
		for _, v := range laces {
			n += int(v)
			if v < 255 {
				complete = true
				break
			}
		}
		if !complete {
			break
		}
		b := read(27+int64(len(laces)), n)
		if len(b) >= 30 && string(b[:7]) == "\x01vorbis" && binary.LittleEndian.Uint32(b[7:11]) == 0 {
			info = AudioInfo{Codec: "Vorbis", SampleRate: int(binary.LittleEndian.Uint32(b[12:16])), Channels: int(b[11])}
		} else if len(b) >= 19 && string(b[:8]) == "OpusHead" && b[8] < 16 {
			// Opus's input-rate field is informational, not the stream clock.
			info = AudioInfo{Codec: "Opus", SampleRate: 48000, Channels: int(b[9])}
		}
	case "M4A":
		remaining := 4096
		var boxes func(int64, int64, int) AudioInfo
		boxes = func(start, end int64, depth int) AudioInfo {
			if depth > 8 {
				return AudioInfo{}
			}
			for off := start; remaining > 0 && off <= end-8; {
				remaining--
				h := read(off, 8)
				if h == nil {
					break
				}
				n, header := int64(binary.BigEndian.Uint32(h[:4])), int64(8)
				if n == 1 {
					x := read(off+8, 8)
					if x == nil || binary.BigEndian.Uint64(x) > uint64(end-off) {
						break
					}
					n, header = int64(binary.BigEndian.Uint64(x)), 16
				} else if n == 0 {
					n = end - off
				}
				if n < header || n > end-off {
					break
				}
				body, limit := off+header, off+n
				switch string(h[4:8]) {
				case "moov", "trak", "mdia", "minf", "stbl":
					if found := boxes(body, limit, depth+1); found.SampleRate > 0 {
						return found
					}
				case "stsd":
					if body+8 <= limit {
						if found := boxes(body+8, limit, depth+1); found.SampleRate > 0 {
							return found
						}
					}
				case "mp4a", "alac":
					b := read(body, 28)
					if body+28 > limit || b == nil {
						break
					}
					found := AudioInfo{Codec: "AAC", Channels: int(binary.BigEndian.Uint16(b[16:18])), SampleRate: int(binary.BigEndian.Uint32(b[24:28]) >> 16)}
					extra := body + 28
					switch binary.BigEndian.Uint16(b[8:10]) {
					case 1:
						extra += 16
					case 0:
					default:
						return AudioInfo{}
					}
					if string(h[4:8]) == "alac" {
						found = AudioInfo{Codec: "ALAC"}
					}
					for pos := extra; remaining > 0 && pos <= limit-8; {
						remaining--
						x := read(pos, 8)
						if x == nil {
							break
						}
						size := int64(binary.BigEndian.Uint32(x[:4]))
						if size < 8 || size > limit-pos {
							break
						}
						if string(x[4:]) == "alac" && size >= 36 {
							if config := read(pos+12, 24); config != nil {
								found.BitDepth, found.Channels, found.SampleRate = int(config[5]), int(config[9]), int(binary.BigEndian.Uint32(config[20:24]))
							}
						} else if string(x[4:]) == "esds" && size >= 12 && size <= 65536 {
							if rate := aacSampleRate(read(pos+12, int(size-12))); rate > 0 {
								found.SampleRate = rate
							}
						}
						pos += size
					}
					return found
				}
				off = limit
			}
			return AudioInfo{}
		}
		info = boxes(0, st.Size(), 0)
	}
	if info.SampleRate < 1 || info.SampleRate > 1536000 {
		info.SampleRate = 0
	}
	if info.BitDepth < 1 || info.BitDepth > 64 {
		info.BitDepth = 0
	}
	if info.Channels < 1 || info.Channels > 256 {
		info.Channels = 0
	}
	return info
}

// Read MPEG-4 descriptors structurally, rather than searching tag bytes inside
// arbitrary metadata. AAC has no source PCM bit depth; sampleSize is ignored.
func aacSampleRate(data []byte) int {
	return aacDescriptorRate(data, 0)
}

func aacDescriptorRate(data []byte, depth int) int {
	if depth > 8 {
		return 0
	}
	for len(data) >= 2 {
		kind, n, used := data[0], 0, 1
		for ; used <= 4 && used < len(data); used++ {
			n = n<<7 | int(data[used]&127)
			if data[used]&128 == 0 {
				used++
				break
			}
		}
		if used > 5 || used > len(data) || data[used-1]&128 != 0 || n > len(data)-used {
			return 0
		}
		b := data[used : used+n]
		switch kind {
		case 3:
			if len(b) < 3 {
				return 0
			}
			skip := 3
			if b[2]&128 != 0 {
				skip += 2
			}
			if b[2]&64 != 0 {
				if skip >= len(b) {
					return 0
				}
				skip += 1 + int(b[skip])
			}
			if b[2]&32 != 0 {
				skip += 2
			}
			if skip <= len(b) {
				return aacDescriptorRate(b[skip:], depth+1)
			}
		case 4:
			if len(b) >= 13 && b[0] == 0x40 {
				return aacDescriptorRate(b[13:], depth+1)
			}
		case 5:
			bit := 0
			bits := func(n int) int {
				if bit+n > len(b)*8 {
					bit = len(b) * 8
					return -1
				}
				v := 0
				for range n {
					v = v<<1 | int(b[bit/8]>>(7-bit%8)&1)
					bit++
				}
				return v
			}
			object := bits(5)
			if object == 31 {
				object = 32 + bits(6)
			}
			rate := func() int {
				i := bits(4)
				if i == 15 {
					return bits(24)
				}
				if i < 0 || i >= 13 {
					return 0
				}
				return []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}[i]
			}
			value := rate()
			if object == 5 || object == 29 {
				bits(4)
				value = rate()
			}
			return value
		}
		data = data[used+n:]
	}
	return 0
}

// Existing drafts get the same header-only metadata on a library scan. Keep
// file IDs, paths, candidate/manual tags and workflow state exactly as stored.
// a.mu is held by the caller; source files are only opened for reading.
func (a *app) refreshDraftAudioInfoLocked() error {
	batches, err := readAll[Batch](a, "batches")
	if err != nil {
		return err
	}
	for _, batch := range batches {
		changed := false
		for i := range batch.Files {
			track := &batch.Files[i]
			if err := regular(a.data, track.Path); err != nil {
				continue
			}
			f, err := a.data.Open(track.Path)
			if err != nil {
				continue
			}
			info := readAudioInfo(f, track.Format)
			_ = f.Close()
			if info != track.AudioInfo {
				track.AudioInfo = info
				changed = true
			}
		}
		if changed {
			if err := a.put("batches", batch.ID, batch); err != nil {
				return err
			}
		}
	}
	return nil
}
