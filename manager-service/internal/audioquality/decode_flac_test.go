package audioquality

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// Build small, original PCM fixtures instead of committing private music.
func flacFixture(t *testing.T, bits int) ([]byte, []int, [][]int32) {
	t.Helper()
	const blockSize = 1024
	samples := [][]int32{noise(2*blockSize+17, 31), noise(2*blockSize+17, 47)}
	var pcm bytes.Buffer
	for i := range samples[0] {
		for c := range samples {
			samples[c][i] %= 1 << (bits - 1)
			var signed [4]byte
			binary.LittleEndian.PutUint32(signed[:], uint32(samples[c][i]))
			pcm.Write(signed[:(bits+7)/8])
		}
	}
	info := &meta.StreamInfo{BlockSizeMin: blockSize, BlockSizeMax: blockSize, SampleRate: 44100, NChannels: 2, BitsPerSample: uint8(bits), NSamples: uint64(len(samples[0])), MD5sum: md5.Sum(pcm.Bytes())}
	var out bytes.Buffer
	enc, err := flac.NewEncoder(&out, info)
	if err != nil {
		t.Fatal(err)
	}
	enc.EnablePredictionAnalysis(false)
	var ends []int
	for offset := 0; offset < len(samples[0]); offset += blockSize {
		end := min(offset+blockSize, len(samples[0]))
		f := &frame.Frame{Header: frame.Header{HasFixedBlockSize: true, BlockSize: uint16(end - offset), SampleRate: 44100, Channels: frame.ChannelsLR, BitsPerSample: uint8(bits)}}
		for _, channel := range samples {
			f.Subframes = append(f.Subframes, &frame.Subframe{SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, Samples: channel[offset:end], NSamples: end - offset})
		}
		if err := enc.WriteFrame(f); err != nil {
			t.Fatal(err)
		}
		ends = append(ends, out.Len())
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), ends, samples
}

func setFLACLength(data []byte, samples uint64) {
	const mask = uint64(1<<36 - 1)
	word := binary.BigEndian.Uint64(data[18:26])
	binary.BigEndian.PutUint64(data[18:26], word&^mask|samples)
}

func TestFLACTrailingDataDoesNotInvalidateCompleteAudio(t *testing.T) {
	data, _, samples := flacFixture(t, 16)
	want := analyzeWAV(t, samples, 44100, 16)
	for name, trailer := range map[string][]byte{
		"none":        nil,
		"opaque":      {0xf0, 0, 0xff, 0x0f, 0x44, 0x44, 0x40, 0x48, 0x46, 0x3c, 0x36, 0x0e, 0x55, 0xff, 0xf0},
		"tag":         append([]byte("TAG"), make([]byte, 125)...),
		"sync_prefix": {0xff, 0xf8},
	} {
		t.Run(name, func(t *testing.T) {
			input := append(bytes.Clone(data), trailer...)
			before := bytes.Clone(input)
			got, err := Analyze(context.Background(), bytes.NewReader(input), "FLAC")
			if err != nil || got != want {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
			if !bytes.Equal(input, before) {
				t.Fatal("analysis modified the source")
			}
		})
	}
}

func TestFLACUnknownLengthAndOptionalChecksum(t *testing.T) {
	for _, bits := range []int{8, 12, 16, 20, 24} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			data, _, samples := flacFixture(t, bits)
			want, err := Analyze(context.Background(), bytes.NewReader(data), "FLAC")
			if err != nil || want.Samples != uint64(len(samples[0])) {
				t.Fatal(want, err)
			}
			for _, knownLength := range []bool{true, false} {
				for _, checksum := range []bool{true, false} {
					input := bytes.Clone(data)
					if !knownLength {
						setFLACLength(input, 0)
					}
					if !checksum {
						clear(input[26:42])
					}
					got, err := Analyze(context.Background(), bytes.NewReader(input), "FLAC")
					if err != nil || got != want {
						t.Fatalf("length=%t checksum=%t: %+v, %v", knownLength, checksum, got, err)
					}
				}
			}
		})
	}
}

func TestFLACCorruptionNeverPublishesPartialMeasurements(t *testing.T) {
	data, ends, _ := flacFixture(t, 16)
	for _, tc := range []struct {
		name    string
		mutate  func([]byte) []byte
		message string
	}{
		{"missing_final_frame", func(b []byte) []byte { return b[:ends[1]] }, "音频不完整"},
		{"truncated_frame", func(b []byte) []byte { return b[:len(b)-1] }, "FLAC 解码失败"},
		{"bad_crc", func(b []byte) []byte { b[ends[0]-1] ^= 1; return b }, "CRC-16"},
		{"bad_sync_midstream", func(b []byte) []byte { b[ends[0]] = 0; return b }, "invalid sync-code"},
		{"checksum_mismatch", func(b []byte) []byte { b[26] ^= 1; return b }, "MD5"},
		{"short_declared_length", func(b []byte) []byte { setFLACLength(b, 1023); return b }, "超过文件头声明"},
		{"false_end_at_frame_boundary", func(b []byte) []byte { setFLACLength(b, 1024); return b }, "MD5"},
		{"false_end_without_checksum", func(b []byte) []byte { setFLACLength(b, 1024); clear(b[26:42]); return b }, "超过文件头声明"},
		{"unverified_trailer", func(b []byte) []byte { clear(b[26:42]); return append(b, []byte("not audio")...) }, "invalid sync-code"},
		{"long_declared_length", func(b []byte) []byte { setFLACLength(b, 10000); return b }, "音频不完整"},
		{"unknown_length_corruption", func(b []byte) []byte { setFLACLength(b, 0); b[ends[0]] = 0; return b }, "invalid sync-code"},
		{"unknown_length_trailer", func(b []byte) []byte { setFLACLength(b, 0); return append(b, []byte("not audio")...) }, "invalid sync-code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.mutate(bytes.Clone(data))
			got, err := Analyze(context.Background(), bytes.NewReader(input), "FLAC")
			if err == nil || !strings.Contains(err.Error(), tc.message) || got != (Result{}) {
				t.Fatalf("corrupt stream produced %+v, %v", got, err)
			}
		})
	}
}
