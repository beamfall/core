package ffmpeg

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ExtractStyledSubtitle remuxes one ASS/SSA stream without flattening its styles.
func (r *Runner) ExtractStyledSubtitle(ctx context.Context, path string, index int) ([]byte, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	args := append([]string{"-v", "error"}, inputProtocolArgs()...)
	args = append(args, "-i", path, "-map", fmt.Sprintf("0:%d", index), "-c:s", "copy", "-f", "ass", "pipe:1")
	out, _, err := r.run(ctx, r.generationCommand(ctx, readConfinement(path), bin, args...))
	return out, err
}

// ErrFontAttachmentNotFound means the requested stream is not a supported font attachment.
var ErrFontAttachmentNotFound = errors.New("font attachment not found")

type attachmentStream struct {
	Index     int               `json:"index"`
	Type      string            `json:"codec_type"`
	Size      int               `json:"extradata_size"`
	Extradata string            `json:"extradata"`
	Tags      map[string]string `json:"tags"`
}

// SubtitleFontIndexes lists font-typed Matroska attachments in stream order,
// with a bounded number and combined size for a single renderer.
func (r *Runner) SubtitleFontIndexes(ctx context.Context, path string) ([]int, error) {
	streams, err := r.probeAttachments(ctx, path, -1)
	if err != nil {
		return nil, err
	}
	indexes := make([]int, 0)
	remaining := 24 << 20
	for _, stream := range streams {
		if len(indexes) == 32 {
			break
		}
		if stream.isFont() && stream.Size <= remaining {
			indexes = append(indexes, stream.Index)
			remaining -= stream.Size
		}
	}
	return indexes, nil
}

// ExtractSubtitleFont checks the stream type and MIME in the same probe that
// reads the payload. ffprobe provides attachment bytes as codec extradata.
func (r *Runner) ExtractSubtitleFont(ctx context.Context, path string, index int) ([]byte, error) {
	streams, err := r.probeAttachments(ctx, path, index)
	if err != nil {
		return nil, err
	}
	if len(streams) != 1 || streams[0].Index != index || !streams[0].isFont() {
		return nil, ErrFontAttachmentNotFound
	}
	stream := streams[0]
	data := make([]byte, 0, stream.Size)
	for line := range strings.SplitSeq(strings.TrimSpace(stream.Extradata), "\n") {
		_, columns, ok := strings.Cut(line, ": ")
		encoded, _, _ := strings.Cut(columns, "  ")
		if !ok || len(encoded) > 39 {
			return nil, ErrFontAttachmentNotFound
		}
		data, err = hex.AppendDecode(data, []byte(strings.ReplaceAll(strings.TrimSpace(encoded), " ", "")))
		if err != nil || len(data) > stream.Size {
			return nil, ErrFontAttachmentNotFound
		}
	}
	if len(data) != stream.Size || len(data) < 12 {
		return nil, ErrFontAttachmentNotFound
	}
	switch string(data[:4]) {
	case "\x00\x01\x00\x00", "true", "ttcf", "OTTO", "wOFF", "wOF2":
		return data, nil
	}
	return nil, ErrFontAttachmentNotFound
}

func (s attachmentStream) isFont() bool {
	if s.Type != "attachment" || s.Size <= 0 || s.Size > 3<<20 {
		return false
	}
	for key, value := range s.Tags {
		if !strings.EqualFold(key, "mimetype") {
			continue
		}
		switch strings.ToLower(value) {
		case "font/ttf", "font/otf", "font/sfnt", "font/collection", "font/woff", "font/woff2",
			"application/x-truetype-font", "application/x-font-ttf", "application/vnd.ms-opentype",
			"application/x-font-opentype", "application/x-font-otf", "application/font-sfnt",
			"application/font-woff", "application/x-font-woff":
			return true
		}
	}
	return false
}

func (r *Runner) probeAttachments(ctx context.Context, path string, index int) ([]attachmentStream, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	bin, err := r.resolveProbe(ctx)
	if err != nil {
		return nil, err
	}
	selector := "t"
	entries := "format=format_name:stream=index,codec_type,extradata_size"
	args := []string{"-v", "error", "-of", "json"}
	if index >= 0 {
		selector = strconv.Itoa(index)
		entries += ",extradata"
		args = append(args, "-show_data")
	}
	args = append(args, "-select_streams", selector, "-show_entries", entries+":stream_tags")
	args = append(args, inputProtocolArgs()...)
	args = append(args, "--", path)
	out, _, err := r.run(ctx, r.hardenedCommand(ctx, bin, readConfinement(path).ReadOnly, nil, args))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Format struct {
			Name string `json:"format_name"`
		} `json:"format"`
		Streams []attachmentStream `json:"streams"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	for name := range strings.SplitSeq(doc.Format.Name, ",") {
		if name == "matroska" {
			return doc.Streams, nil
		}
	}
	return nil, nil
}
