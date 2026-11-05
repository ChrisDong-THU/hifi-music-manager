package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAudioInfoHeaders(t *testing.T) {
	flac := func(rate, depth, channels int) []byte {
		b := make([]byte, 42)
		copy(b, "fLaC\x80\x00\x00\x22")
		binary.BigEndian.PutUint64(b[18:26], uint64(rate)<<44|uint64(channels-1)<<41|uint64(depth-1)<<36)
		return b
	}
	wave := func(extensible bool) []byte {
		b := make([]byte, 16)
		binary.LittleEndian.PutUint16(b, 1)
		binary.LittleEndian.PutUint16(b[2:], 2)
		binary.LittleEndian.PutUint32(b[4:], 96000)
		binary.LittleEndian.PutUint16(b[14:], 24)
		if extensible {
			b = append(b, make([]byte, 24)...)
			binary.LittleEndian.PutUint16(b, 0xfffe)
			binary.LittleEndian.PutUint16(b[14:], 32)
			binary.LittleEndian.PutUint16(b[16:], 22)
			binary.LittleEndian.PutUint16(b[18:], 24)
			copy(b[24:], []byte{1, 0, 0, 0, 0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113})
		}
		var out bytes.Buffer
		out.WriteString("RIFF")
		_ = binary.Write(&out, binary.LittleEndian, uint32(4+8+2+8+len(b)))
		out.WriteString("WAVEJUNK\x01\x00\x00\x00x\x00fmt ")
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(b)))
		out.Write(b)
		return out.Bytes()
	}
	ogg := func(packet []byte) []byte {
		b := make([]byte, 28)
		copy(b, "OggS")
		b[5], b[26], b[27] = 2, 1, byte(len(packet))
		return append(b, packet...)
	}
	vorbis := make([]byte, 30)
	copy(vorbis, "\x01vorbis")
	vorbis[11] = 2
	binary.LittleEndian.PutUint32(vorbis[12:], 44100)
	opus := make([]byte, 19)
	copy(opus, "OpusHead")
	opus[8], opus[9] = 1, 2
	binary.LittleEndian.PutUint32(opus[12:], 44100) // Must not be treated as encoded rate.
	aiff := make([]byte, 38)
	copy(aiff, "FORM\x00\x00\x00\x1eAIFFCOMM\x00\x00\x00\x12")
	binary.BigEndian.PutUint16(aiff[20:], 2)
	binary.BigEndian.PutUint16(aiff[26:], 24)
	copy(aiff[28:], []byte{0x40, 0x0e, 0xac, 0x44, 0, 0, 0, 0, 0, 0}) // 80-bit 44100.
	atom := func(kind string, payload []byte) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
		copy(b[4:], kind)
		return append(b, payload...)
	}
	m4a := func(codec string, config []byte) []byte {
		entry := make([]byte, 28)
		binary.BigEndian.PutUint16(entry[16:], 2)
		binary.BigEndian.PutUint16(entry[18:], 16) // AAC sampleSize is not source precision.
		binary.BigEndian.PutUint32(entry[24:], 44100<<16)
		entry = append(entry, config...)
		stsd := append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, atom(codec, entry)...)
		return atom("moov", atom("trak", atom("mdia", atom("minf", atom("stbl", atom("stsd", stsd))))))
	}
	alac := make([]byte, 28)
	alac[9], alac[13] = 24, 2 // Four fullbox bytes precede ALACSpecificConfig.
	binary.BigEndian.PutUint32(alac[24:], 192000)
	// DecoderConfigDescriptor followed by AudioSpecificConfig: AAC-LC / 96kHz / stereo.
	decoder := append([]byte{0x40, 0x15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{5, 2, 0x10, 0x10}...)
	esds := atom("esds", append([]byte{0, 0, 0, 0, 3, byte(3 + 2 + len(decoder)), 0, 1, 0, 4, byte(len(decoder))}, decoder...))
	cases := []struct {
		name, format string
		data         []byte
		want         AudioInfo
	}{
		{"flac16", "FLAC", flac(44100, 16, 2), AudioInfo{SampleRate: 44100, BitDepth: 16, Channels: 2}},
		{"flac24", "FLAC", flac(44100, 24, 2), AudioInfo{SampleRate: 44100, BitDepth: 24, Channels: 2}},
		{"flac192", "FLAC", flac(192000, 24, 6), AudioInfo{SampleRate: 192000, BitDepth: 24, Channels: 6}},
		{"wav24-odd-padding", "WAV", wave(false), AudioInfo{SampleRate: 96000, BitDepth: 24, Channels: 2}},
		{"wav24-in-32", "WAV", wave(true), AudioInfo{SampleRate: 96000, BitDepth: 24, Channels: 2}},
		{"aiff24", "AIFF", aiff, AudioInfo{SampleRate: 44100, BitDepth: 24, Channels: 2}},
		{"mp3", "MP3", []byte{0xff, 0xfb, 0x90, 0}, AudioInfo{SampleRate: 44100, Channels: 2}},
		{"mp3-mpeg2-mono", "MP3", []byte{0xff, 0xf3, 0x84, 0xc0}, AudioInfo{SampleRate: 24000, Channels: 1}},
		{"mp3-mpeg25", "MP3", []byte{0xff, 0xe3, 0x88, 0}, AudioInfo{SampleRate: 8000, Channels: 2}},
		{"mp3-id3", "MP3", append([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0}, []byte{0xff, 0xfb, 0x90, 0}...), AudioInfo{SampleRate: 44100, Channels: 2}},
		{"vorbis", "OGG", ogg(vorbis), AudioInfo{Codec: "Vorbis", SampleRate: 44100, Channels: 2}},
		{"opus", "OPUS", ogg(opus), AudioInfo{Codec: "Opus", SampleRate: 48000, Channels: 2}},
		{"aac", "M4A", m4a("mp4a", nil), AudioInfo{Codec: "AAC", SampleRate: 44100, Channels: 2}},
		{"aac96-config", "M4A", m4a("mp4a", esds), AudioInfo{Codec: "AAC", SampleRate: 96000, Channels: 2}},
		{"alac192", "M4A", m4a("alac", atom("alac", alac)), AudioInfo{Codec: "ALAC", SampleRate: 192000, BitDepth: 24, Channels: 2}},
		{"truncated-flac", "FLAC", []byte("fLaC"), AudioInfo{}},
		{"bad-wav-chunk", "WAV", []byte("RIFF\xff\xff\xff\x7fWAVEfmt \xff\xff\xff\x7f"), AudioInfo{}},
		{"bad-mp4-size", "M4A", []byte{0, 0, 0, 4, 'm', 'o', 'o', 'v'}, AudioInfo{}},
		{"bad-mp3-rate", "MP3", []byte{0xff, 0xfb, 0x9c, 0}, AudioInfo{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "audio")
			if err := os.WriteFile(p, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if got := readAudioInfo(f, tc.format); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAudioInfoImportAndExistingDrafts(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "音质测试", 123)
	want := AudioInfo{SampleRate: 44100, BitDepth: 16, Channels: 1}
	if b.Files[0].AudioInfo != want {
		t.Fatalf("upload inspection: %+v", b.Files[0].AudioInfo)
	}
	// Simulate an existing draft with manual tags and no quality fields.
	b.Files[0].AudioInfo = AudioInfo{}
	b.Candidates = []Candidate{{ID: "manual", Source: "manual", Tags: []Tags{{Title: "手动标题"}}}}
	b.ChosenCandidate = "manual"
	before := b
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(); err != nil {
		t.Fatal(err)
	}
	after, err := get[Batch](a, "batches", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Files[0].AudioInfo != want {
		t.Fatalf("backfill: %+v", after.Files[0].AudioInfo)
	}
	after.Files[0].AudioInfo = AudioInfo{}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("quality backfill modified tags, IDs or workflow state")
	}
	// Re-read and import through the existing file-tag workflow.
	if err := a.localPreview(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	tracks := a.tracks()
	if len(tracks) != 1 || tracks[0].AudioInfo != want {
		t.Fatalf("import: %+v", tracks)
	}
	if tracks[0].AudioHash != b.Files[0].AudioHash {
		t.Fatal("quality detection modified audio")
	}
	id := tracks[0].ID
	if err := a.scan(); err != nil {
		t.Fatal(err)
	}
	if tracks = a.tracks(); len(tracks) != 1 || tracks[0].ID != id || tracks[0].AudioInfo != want {
		t.Fatal("scan lost quality or changed track ID")
	}
	raw, _ := json.Marshal(tracks[0])
	var response map[string]any
	_ = json.Unmarshal(raw, &response)
	if response["sample_rate"] != float64(44100) || response["bit_depth"] != float64(16) {
		t.Fatalf("API JSON missing quality: %s", raw)
	}
}
