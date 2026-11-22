package lyrics

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func starts(doc Document) []int {
	out := make([]int, len(doc.Lines))
	for i, line := range doc.Lines {
		if line.StartMs == nil {
			out[i] = -1
			continue
		}
		out[i] = *line.StartMs
	}
	return out
}

func texts(doc Document) []string {
	out := make([]string, len(doc.Lines))
	for i, line := range doc.Lines {
		out[i] = line.Text
	}
	return out
}

func equal[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseLRC(t *testing.T) {
	doc := Parse("\uFEFF[ar:Someone]\r\n[ti:Title]\r\n[offset:+500]\r\n[00:12.50]First <00:12.80>line\r\n" +
		"[00:20.00][01:05.3]Chorus\r\nan untimed note\r\n[00:01:07]Start\r\n[00:00.20]Clamped\r\n")
	if !doc.Synced {
		t.Fatal("want synced")
	}
	equal(t, texts(doc), []string{"Clamped", "Start", "First line", "Chorus", "Chorus"})
	equal(t, starts(doc), []int{0, 570, 12000, 19500, 64800})
}

func TestParsePlain(t *testing.T) {
	doc := Parse("\n\n  Verse one  \nline two\n\nVerse two\n\n")
	if doc.Synced {
		t.Fatal("want plain")
	}
	equal(t, texts(doc), []string{"Verse one", "line two", "", "Verse two"})
	equal(t, starts(doc), []int{-1, -1, -1, -1})
	if !Parse(" \n\t\n").Empty() {
		t.Fatal("blank text should be empty")
	}
}

func TestParseCapsLines(t *testing.T) {
	doc := Parse(strings.Repeat("line\n", MaxLines+50))
	if len(doc.Lines) != MaxLines {
		t.Fatalf("lines = %d", len(doc.Lines))
	}
}

// id3Tag builds an ID3v2 tag of the given version holding frames.
func id3Tag(version byte, frames ...[]byte) []byte {
	body := bytes.Join(frames, nil)
	head := []byte{'I', 'D', '3', version, 0, 0}
	n := len(body)
	head = append(head, byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f))
	return append(head, body...)
}

func frame(version byte, id string, body []byte) []byte {
	n := len(body)
	out := []byte(id)
	if version == 4 {
		out = append(out, byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f))
	} else {
		out = binary.BigEndian.AppendUint32(out, uint32(n)) // #nosec G115 -- test frames are a few bytes
	}
	out = append(out, 0, 0)
	return append(out, body...)
}

func utf16BOM(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func usltUTF16(text string) []byte {
	body := append([]byte{1}, "eng"...)
	body = append(body, utf16BOM("")...)
	body = append(body, 0, 0)
	return append(body, utf16BOM(text)...)
}

type syltEntry struct {
	text string
	ms   uint32
}

func syltUTF8(entries ...syltEntry) []byte {
	body := append([]byte{3}, "eng"...)
	body = append(body, 2, 1, 0) // ms stamps, lyrics content, empty descriptor
	for _, e := range entries {
		body = append(body, e.text...)
		body = append(body, 0)
		body = binary.BigEndian.AppendUint32(body, e.ms)
	}
	return body
}

func TestReadID3Frames(t *testing.T) {
	for _, version := range []byte{3, 4} {
		tag := id3Tag(version,
			frame(version, "TIT2", []byte{3, 'x'}),
			frame(version, "USLT", usltUTF16("Plain words\nsecond")),
			frame(version, "SYLT", syltUTF8(syltEntry{"Hello ", 1000}, syltEntry{"world", 1400}, syltEntry{"\nNext", 3000})),
		)
		got, err := readID3(bytes.NewReader(tag))
		if err != nil {
			t.Fatalf("v2.%d: %v", version, err)
		}
		if got.unsynced != "Plain words\nsecond" {
			t.Fatalf("v2.%d unsynced = %q", version, got.unsynced)
		}
		equal(t, texts(got.synced), []string{"Hello world", "Next"})
		equal(t, starts(got.synced), []int{1000, 3000})
	}
}

func TestSYLTEntryPerLineAndFrameStamps(t *testing.T) {
	doc := parseSYLT(syltUTF8(syltEntry{"One", 10}, syltEntry{"Two", 20}))
	equal(t, texts(doc), []string{"One", "Two"})
	body := syltUTF8(syltEntry{"One", 10})
	body[4] = 1 // MPEG frame stamps are not supported
	if !parseSYLT(body).Empty() {
		t.Fatal("frame-stamped SYLT should be ignored")
	}
}

func TestReadID3RejectsGarbage(t *testing.T) {
	if _, err := readID3(bytes.NewReader([]byte("fLaC...."))); err == nil {
		t.Fatal("want error for a non-ID3 file")
	}
	tag := id3Tag(3, frame(3, "USLT", usltUTF16("ok")))
	tag = append(tag[:len(tag)-4], 0xff) // truncated
	if _, err := readID3(bytes.NewReader(tag)); err == nil {
		t.Fatal("want error for a truncated tag")
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	track := filepath.Join(dir, "01 Song.mp3")
	write(t, track, append(id3Tag(4,
		frame(4, "USLT", usltUTF16("embedded plain")),
		frame(4, "SYLT", syltUTF8(syltEntry{"embedded synced", 500})),
	), make([]byte, 64)...))
	write(t, filepath.Join(dir, "01 Song.txt"), []byte("sidecar text"))

	check := func(field, wantSource, wantFirst string) {
		t.Helper()
		doc, source := Resolve(track, field)
		if source != wantSource || len(doc.Lines) == 0 || doc.Lines[0].Text != wantFirst {
			t.Fatalf("source %q lines %v, want %q %q", source, texts(doc), wantSource, wantFirst)
		}
	}
	check("field words", SourceEmbedded, "embedded synced")

	write(t, filepath.Join(dir, "01 Song.lrc"), []byte("[00:01.00]sidecar synced"))
	check("field words", SourceSidecarLRC, "sidecar synced")

	plain := filepath.Join(dir, "02 Plain.mp3")
	write(t, plain, append(id3Tag(3, frame(3, "USLT", usltUTF16("embedded plain"))), 0))
	check2 := func(field, wantSource, wantFirst string) {
		t.Helper()
		doc, source := Resolve(plain, field)
		if source != wantSource || len(doc.Lines) == 0 || doc.Lines[0].Text != wantFirst {
			t.Fatalf("source %q lines %v, want %q %q", source, texts(doc), wantSource, wantFirst)
		}
	}
	check2("[00:02.00]field synced", SourceMetadata, "field synced")
	check2("", SourceEmbedded, "embedded plain")

	bare := filepath.Join(dir, "03 Bare.flac")
	write(t, bare, []byte("fLaC"))
	write(t, filepath.Join(dir, "03 Bare.txt"), []byte("\nplain sidecar\n"))
	doc, source := Resolve(bare, "")
	if source != SourceSidecarTxt || doc.Synced || doc.Lines[0].Text != "plain sidecar" {
		t.Fatalf("got %q %v", source, texts(doc))
	}

	none := filepath.Join(dir, "04 None.flac")
	write(t, none, []byte("fLaC"))
	if doc, source := Resolve(none, ""); source != "" || len(doc.Lines) != 0 {
		t.Fatalf("got %q %v", source, texts(doc))
	}
}

func TestSidecarRefusesSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.lrc")
	write(t, outside, []byte("[00:01.00]outside"))
	track := filepath.Join(dir, "song.flac")
	write(t, track, []byte("fLaC"))
	if err := os.Symlink(outside, filepath.Join(dir, "song.lrc")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if got := readSidecar(track, ".lrc"); got != "" {
		t.Fatalf("symlinked sidecar read: %q", got)
	}
	write(t, filepath.Join(dir, "song.txt"), bytes.Repeat([]byte("a"), maxSidecarBytes+1))
	if got := readSidecar(track, ".txt"); got != "" {
		t.Fatal("oversize sidecar read")
	}
	if got := readSidecar(filepath.Join(dir, "song.lrc"), ".lrc"); got != "" {
		t.Fatal("a sidecar must not be its own primary")
	}
}
