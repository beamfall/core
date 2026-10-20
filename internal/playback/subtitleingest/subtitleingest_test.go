package subtitleingest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestClassifyWhitelistAndRejections(t *testing.T) {
	for _, ext := range []string{"srt", "vtt", "ass", "ssa", "sup", "idx", "sub", "smi", "ttml"} {
		if _, ok := Classify(ext); !ok {
			t.Errorf("Classify(%q) = not allowed, want allowed", ext)
		}
	}
	// MicroDVD/.txt and unknown containers are rejected by omission.
	for _, ext := range []string{"txt", "microdvd", "mp4", "exe", ""} {
		if _, ok := Classify(ext); ok {
			t.Errorf("Classify(%q) = allowed, want rejected", ext)
		}
	}
	// Normalised defensively: leading dot and upper case still classify.
	if _, ok := Classify(".ASS"); !ok {
		t.Errorf("Classify(.ASS) = not allowed, want allowed after normalisation")
	}
}

func TestGuardRejectsUnknownFormat(t *testing.T) {
	if err := Guard("txt", []byte("00:00:01,000\nhi")); !errors.Is(err, ErrFormatNotAllowed) {
		t.Fatalf("Guard(txt) err = %v, want ErrFormatNotAllowed", err)
	}
}

func TestGuardRejectsOversize(t *testing.T) {
	big := bytes.Repeat([]byte("A"), MaxTextBytes+1)
	if err := Guard("srt", big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Guard(oversize srt) err = %v, want ErrTooLarge", err)
	}
	// A bitmap sidecar under its (larger) cap passes the size gate.
	bmp := bytes.Repeat([]byte("A"), MaxTextBytes+1)
	if err := Guard("sup", bmp); err != nil {
		t.Fatalf("Guard(sup under bitmap cap) err = %v, want nil", err)
	}
	if err := Guard("sup", bytes.Repeat([]byte("A"), MaxBitmapBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Guard(oversize sup) err = %v, want ErrTooLarge", err)
	}
}

func TestGuardAcceptsWellFormed(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	if err := Guard("srt", []byte(srt)); err != nil {
		t.Fatalf("Guard(well-formed srt) err = %v, want nil", err)
	}
	ass := "[Script Info]\n[Events]\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\\i1}Hi{\\i0}\n"
	if err := Guard("ass", []byte(ass)); err != nil {
		t.Fatalf("Guard(well-formed ass) err = %v, want nil", err)
	}
}

func TestGuardRejectsCraftedASS(t *testing.T) {
	// Line-length bomb: one physical line far past the cap.
	bomb := "Dialogue: " + strings.Repeat("A", MaxASSLineBytes+10) + "\n"
	if err := Guard("ass", []byte(bomb)); !errors.Is(err, ErrCraftedASS) {
		t.Fatalf("Guard(line-bomb ass) err = %v, want ErrCraftedASS", err)
	}
	// Override-tag flood inside one brace group.
	flood := "Dialogue: 0,,,,{" + strings.Repeat("\\b1", MaxASSOverrideTagsPerLine+5) + "}x\n"
	if err := Guard("ass", []byte(flood)); !errors.Is(err, ErrCraftedASS) {
		t.Fatalf("Guard(tag-flood ass) err = %v, want ErrCraftedASS", err)
	}
	// Unbounded drawing-mode coordinate blob.
	draw := "Dialogue: 0,,,,{\\p1}" + strings.Repeat("1 2 ", MaxASSDrawingRunBytes) + "\n"
	if err := Guard("ass", []byte(draw)); !errors.Is(err, ErrCraftedASS) {
		t.Fatalf("Guard(drawing-blob ass) err = %v, want ErrCraftedASS", err)
	}
}

// Each scope limit must bite on its OWN terms. A fixture sized from the constant
// it exercises ("Repeat(\"1 2 \", MaxASSDrawingRunBytes)" — 64 KiB of payload)
// overshoots the 64 KiB LINE cap, so the line check rejects it first and the
// limit under test is never reached: delete that limit and the test still
// passes. Every fixture here is sized independently of its constant and asserts
// it stays inside one line's budget, so it fails when — and only when — the
// limit it targets is gone.
func TestCheckASSLimitsBiteWellInsideTheLineCap(t *testing.T) {
	// A 20 000-byte drawing run in a ~20 KB line: under a third of the line cap.
	drawing := "Dialogue: 0,,,,{\\p1}" + strings.Repeat("1 2 ", 5000) + "\n"
	assertUnderLineCap(t, "drawing blob", drawing)
	if err := Guard("ass", []byte(drawing)); !errors.Is(err, ErrCraftedASS) {
		t.Fatalf("Guard(drawing blob inside the line cap) err = %v, want ErrCraftedASS", err)
	}
	// The same blob parked INSIDE the brace group. Drawing mode is open either
	// way, so the run limit applies either way — the attacker picks the shape.
	grouped := "Dialogue: 0,,,,{\\p1" + strings.Repeat("1 2 ", 5000) + "}\n"
	assertUnderLineCap(t, "grouped drawing blob", grouped)
	if err := Guard("ass", []byte(grouped)); !errors.Is(err, ErrCraftedASS) {
		t.Fatalf("Guard(drawing blob inside a brace group) err = %v, want ErrCraftedASS", err)
	}
	// 400 override tags in a ~1.6 KB line: two orders of magnitude under the cap.
	flood := "Dialogue: 0,,,,{" + strings.Repeat("\\b1", 400) + "}x\n"
	assertUnderLineCap(t, "tag flood", flood)
	if err := Guard("ass", []byte(flood)); !errors.Is(err, ErrCraftedASS) {
		t.Fatalf("Guard(tag flood inside the line cap) err = %v, want ErrCraftedASS", err)
	}
	// A legitimately authored line — a small vector sign plus styling — is still
	// accepted, so the tightened scope is not merely rejecting everything.
	authored := "Dialogue: 0,,,,{\\p1}m 0 0 l 100 0 l 100 100 l 0 100{\\p0}{\\i1}Hi{\\i0}\n"
	if err := Guard("ass", []byte(authored)); err != nil {
		t.Fatalf("Guard(authored sign + styling) err = %v, want nil", err)
	}
}

// assertUnderLineCap fails the test when a crafted-ASS fixture is long enough
// for the line-length check to reject it — which would make the fixture prove
// nothing about the limit it is supposed to exercise.
func assertUnderLineCap(t *testing.T, label, fixture string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSuffix(fixture, "\n"), "\n") {
		if len(line) > MaxASSLineBytes {
			t.Fatalf("%s fixture line is %d bytes, over the %d-byte line cap: the line check would reject it first", label, len(line), MaxASSLineBytes)
		}
	}
}

func TestValidateZipEntryNameRejectsSlip(t *testing.T) {
	bad := []string{
		"../etc/passwd",
		"../../a.srt",
		"/abs/a.srt",
		"a\\..\\b.srt",
		"C:\\evil.srt",
		"",
	}
	for _, name := range bad {
		if err := ValidateZipEntryName(name); !errors.Is(err, ErrZipSlip) {
			t.Errorf("ValidateZipEntryName(%q) err = %v, want ErrZipSlip", name, err)
		}
	}
	for _, name := range []string{"a.srt", "sub/a.en.srt", "deep/nested/movie.ass"} {
		if err := ValidateZipEntryName(name); err != nil {
			t.Errorf("ValidateZipEntryName(%q) err = %v, want nil", name, err)
		}
	}
}

func TestCheckDecompressionRatioRejectsBomb(t *testing.T) {
	if err := CheckDecompressionRatio(10, uint64(10*MaxDecompressionRatio+10)); !errors.Is(err, ErrZipBomb) {
		t.Fatalf("ratio bomb err = %v, want ErrZipBomb", err)
	}
	if err := CheckDecompressionRatio(0, 1); !errors.Is(err, ErrZipBomb) {
		t.Fatalf("zero-compressed bomb err = %v, want ErrZipBomb", err)
	}
	if err := CheckDecompressionRatio(1, MaxArchiveEntryBytes+1); !errors.Is(err, ErrZipBomb) {
		t.Fatalf("over-cap entry err = %v, want ErrZipBomb", err)
	}
	if err := CheckDecompressionRatio(1000, 2000); err != nil {
		t.Fatalf("modest expansion err = %v, want nil", err)
	}
	if err := CheckDecompressionRatio(10, uint64(10*MaxDecompressionRatio+9)); !errors.Is(err, ErrZipBomb) {
		t.Fatalf("fractional over-ratio err = %v, want ErrZipBomb", err)
	}
	if err := CheckDecompressionRatio(0, 0); err != nil {
		t.Fatalf("empty entry err = %v, want nil", err)
	}
}

func TestInspectArchiveRejectsBombBeforeExtract(t *testing.T) {
	// A well-formed archive with one small subtitle passes.
	good := buildZip(t, map[string]string{"movie.en.srt": "1\n00:00:01,000 --> 00:00:02,000\nhi\n"})
	if err := InspectArchive(good); err != nil {
		t.Fatalf("InspectArchive(good) err = %v, want nil", err)
	}
	// A zip-slip entry name is rejected from the central directory alone.
	slip := buildZip(t, map[string]string{"../evil.srt": "x"})
	if err := InspectArchive(slip); !errors.Is(err, ErrZipSlip) {
		t.Fatalf("InspectArchive(slip) err = %v, want ErrZipSlip", err)
	}
}

func TestInspectArchiveRejectsSymlinkEntriesBeforeDirectoryExemption(t *testing.T) {
	if err := InspectArchive(nil); !errors.Is(err, ErrUnsafeArchiveEntry) {
		t.Fatalf("InspectArchive(nil) err = %v, want ErrUnsafeArchiveEntry", err)
	}
	for _, name := range []string{"evil.srt", "evil.srt/"} {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(os.ModeSymlink | 0o777)
		zr := &zip.Reader{File: []*zip.File{{FileHeader: *header}}}
		if err := InspectArchive(zr); !errors.Is(err, ErrUnsafeArchiveEntry) {
			t.Fatalf("InspectArchive(symlink %q) err = %v, want ErrUnsafeArchiveEntry", name, err)
		}
	}
	pipe := &zip.FileHeader{Name: "evil.srt/", Method: zip.Store}
	pipe.SetMode(os.ModeNamedPipe | 0o666)
	if err := InspectArchive(&zip.Reader{File: []*zip.File{{FileHeader: *pipe}}}); !errors.Is(err, ErrUnsafeArchiveEntry) {
		t.Fatalf("InspectArchive(trailing-slash pipe) err = %v, want ErrUnsafeArchiveEntry", err)
	}
}

func TestInspectArchiveRejectsEntryCountAndAggregateDeclaredSize(t *testing.T) {
	tooMany := syntheticZip(MaxArchiveEntries+1, 0)
	if err := InspectArchive(tooMany); !errors.Is(err, ErrArchiveLimit) {
		t.Fatalf("InspectArchive(%d entries) err = %v, want ErrArchiveLimit", MaxArchiveEntries+1, err)
	}

	entrySize := uint64(MaxArchiveEntryBytes)
	entryCount := MaxArchiveTotalBytes/MaxArchiveEntryBytes + 1
	tooLarge := syntheticZip(entryCount, entrySize)
	for _, entry := range tooLarge.File {
		if err := CheckDecompressionRatio(entry.CompressedSize64, entry.UncompressedSize64); err != nil {
			t.Fatalf("per-entry guard unexpectedly rejected aggregate fixture: %v", err)
		}
	}
	if err := InspectArchive(tooLarge); !errors.Is(err, ErrArchiveLimit) {
		t.Fatalf("InspectArchive(%d declared bytes) err = %v, want ErrArchiveLimit",
			uint64(entryCount)*entrySize, err)
	}
}

func TestReadArchiveEntryBoundsInflationWhenCentralDirectoryLies(t *testing.T) {
	payload := bytes.Repeat([]byte("A"), MaxTextBytes+4096)
	archive := buildZipBytes(t, []zipFixture{{
		header:  &zip.FileHeader{Name: "movie.srt", Method: zip.Store},
		content: string(payload),
	}})
	patchCentralDirectoryUncompressedSize(t, archive, MaxTextBytes)
	bodyStart := zipBodyStart(t, archive)
	counted := &countingReaderAt{
		reader:    bytes.NewReader(archive),
		bodyStart: bodyStart,
		bodyEnd:   bodyStart + int64(len(payload)),
	}
	zr, err := zip.NewReader(counted, int64(len(archive)))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	if err := InspectArchive(zr); err != nil {
		t.Fatalf("dishonest central directory should pass declared-size inspection: %v", err)
	}
	counted.bodyBytes = 0
	if _, err := ReadArchiveEntry(zr.File[0]); !errors.Is(err, ErrZipBomb) {
		t.Fatalf("ReadArchiveEntry(dishonest size) err = %v, want ErrZipBomb", err)
	}
	if counted.bodyBytes > MaxTextBytes+1 {
		t.Fatalf("extracted %d body bytes, want at most %d", counted.bodyBytes, MaxTextBytes+1)
	}
	if counted.bodyBytes >= int64(len(payload)) {
		t.Fatalf("lying-size rejection consumed the entire %d-byte payload", len(payload))
	}
}

func TestSanitizeWebVTTDefangsMarkup(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n<i>hi</i> <script>alert(1)</script> <img onerror=x>\n"
	out := SanitizeWebVTT(in)
	if strings.Contains(out, "<script>") || strings.Contains(out, "<img") {
		t.Fatalf("sanitized output still carries raw markup: %q", out)
	}
	if !strings.Contains(out, "<i>hi</i>") {
		t.Fatalf("sanitized output dropped allowed <i> tag: %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("sanitized output did not escape <script>: %q", out)
	}
	// Timing lines and header survive untouched.
	if !strings.Contains(out, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("sanitizer corrupted the cue timing line: %q", out)
	}
}

func TestSanitizeWebVTTPreservesAllowedTags(t *testing.T) {
	in := "<c.loud>loud</c> <v Bob>hi</v> <00:00:01.500>ts <lang en>x</lang> <b><u>bu</u></b>\n"
	out := SanitizeWebVTT(in)
	if out != in {
		t.Fatalf("sanitizer altered an all-allowed cue:\n in=%q\nout=%q", in, out)
	}
}

func TestSanitizeWebVTTIsIdempotent(t *testing.T) {
	in := "<i>ok</i> <script>x</script>"
	once := SanitizeWebVTT(in)
	twice := SanitizeWebVTT(once)
	if once != twice {
		t.Fatalf("SanitizeWebVTT not idempotent:\nonce=%q\ntwice=%q", once, twice)
	}
}

type zipFixture struct {
	header  *zip.FileHeader
	content string
}

type countingReaderAt struct {
	reader    io.ReaderAt
	bodyStart int64
	bodyEnd   int64
	bodyBytes int64
}

func (r *countingReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.reader.ReadAt(p, offset)
	readStart := max(offset, r.bodyStart)
	readEnd := min(offset+int64(n), r.bodyEnd)
	if readEnd > readStart {
		r.bodyBytes += readEnd - readStart
	}
	return n, err
}

func syntheticZip(entries int, size uint64) *zip.Reader {
	files := make([]*zip.File, entries)
	for i := range files {
		files[i] = &zip.File{FileHeader: zip.FileHeader{
			Name:               fmt.Sprintf("subtitle-%03d.sup", i),
			Method:             zip.Store,
			CompressedSize64:   size,
			UncompressedSize64: size,
		}}
	}
	return &zip.Reader{File: files}
}

func buildZipBytes(t *testing.T, entries []zipFixture) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.CreateHeader(entry.header)
		if err != nil {
			t.Fatalf("zip create %q: %v", entry.header.Name, err)
		}
		if _, err := w.Write([]byte(entry.content)); err != nil {
			t.Fatalf("zip write %q: %v", entry.header.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func patchCentralDirectoryUncompressedSize(t *testing.T, archive []byte, size uint32) {
	t.Helper()
	const centralDirectoryHeader = "PK\x01\x02"
	offset := bytes.Index(archive, []byte(centralDirectoryHeader))
	if offset < 0 {
		t.Fatal("central-directory header not found")
	}
	binary.LittleEndian.PutUint32(archive[offset+24:offset+28], size)
}

func zipBodyStart(t *testing.T, archive []byte) int64 {
	t.Helper()
	const localHeaderFixedBytes = 30
	if len(archive) < localHeaderFixedBytes || string(archive[:4]) != "PK\x03\x04" {
		t.Fatal("local ZIP header not found")
	}
	nameBytes := binary.LittleEndian.Uint16(archive[26:28])
	extraBytes := binary.LittleEndian.Uint16(archive[28:30])
	return localHeaderFixedBytes + int64(nameBytes) + int64(extraBytes)
}

// buildZip writes an in-memory zip archive with the given name→content entries
// and returns a reader over it, for the archive-inspection tests.
func buildZip(t *testing.T, entries map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	return zr
}
