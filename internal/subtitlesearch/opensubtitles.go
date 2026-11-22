package subtitlesearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// OpenSubtitlesID is the OpenSubtitles provider's slug and outbound policy ID.
const OpenSubtitlesID = "opensubtitles"

// OpenSubtitlesAPIOrigin is the REST API origin. Download links are served
// from OpenSubtitlesDownloadOrigins.
const OpenSubtitlesAPIOrigin = "https://api.opensubtitles.com"

// OpenSubtitlesDownloadOrigins are the origins the download endpoint hands out
// links on. A link on any other origin is refused before a request is made.
var OpenSubtitlesDownloadOrigins = []string{"https://www.opensubtitles.com", "https://dl.opensubtitles.org"}

// OpenSubtitlesKeyRef is the credential ref the API key resolves under: the
// operator's 0600 file <data-dir>/secrets/opensubtitles-api-key, then a key a
// release build injected.
const OpenSubtitlesKeyRef = "opensubtitles-api-key"

// KeySource returns the API key. An fs.ErrNotExist error means no key is
// configured.
type KeySource func() (string, error)

// OpenSubtitles is the OpenSubtitles.com REST v1 provider.
type OpenSubtitles struct {
	egress    Egress
	key       KeySource
	userAgent string
}

// NewOpenSubtitles returns the provider. userAgent names the client
// ("Beamfall v1.2.3") and carries no install identifier.
func NewOpenSubtitles(egress Egress, key KeySource, userAgent string) *OpenSubtitles {
	return &OpenSubtitles{egress: egress, key: key, userAgent: userAgent}
}

// ID implements Provider.
func (o *OpenSubtitles) ID() string { return OpenSubtitlesID }

// Ready implements Provider: true when a key is configured.
func (o *OpenSubtitles) Ready(context.Context) bool {
	_, err := o.apiKey()
	return err == nil
}

func (o *OpenSubtitles) apiKey() (string, error) {
	if o.key == nil || o.egress == nil {
		return "", ErrNoKey
	}
	key, err := o.key()
	if errors.Is(err, fs.ErrNotExist) || (err == nil && strings.TrimSpace(key) == "") {
		return "", ErrNoKey
	}
	if err != nil {
		return "", fmt.Errorf("opensubtitles key: %w", err)
	}
	return strings.TrimSpace(key), nil
}

func (o *OpenSubtitles) header(key string, body bool) map[string][]string {
	h := map[string][]string{
		"Api-Key":    {key},
		"User-Agent": {o.userAgent},
		"Accept":     {"application/json"},
	}
	if body {
		h["Content-Type"] = []string{"application/json"}
	}
	return h
}

type osSearchResponse struct {
	Data []struct {
		Attributes struct {
			Language        string  `json:"language"`
			DownloadCount   int     `json:"download_count"`
			HearingImpaired bool    `json:"hearing_impaired"`
			ForeignParts    bool    `json:"foreign_parts_only"`
			Ratings         float64 `json:"ratings"`
			MoviehashMatch  bool    `json:"moviehash_match"`
			Release         string  `json:"release"`
			Files           []struct {
				FileID   int64  `json:"file_id"`
				FileName string `json:"file_name"`
			} `json:"files"`
		} `json:"attributes"`
	} `json:"data"`
}

// SearchURL builds the search request URL. Parameters are lowercase and in
// alphabetical order, as the API asks, so it answers without a redirect.
func SearchURL(q Query) (string, bool) {
	values := url.Values{}
	if hash := strings.ToLower(strings.TrimSpace(q.OSHash)); validOSHash(hash) {
		values.Set("moviehash", hash)
	}
	if title := strings.ToLower(strings.TrimSpace(q.Title)); title != "" {
		values.Set("query", title)
	}
	if values.Get("moviehash") == "" && values.Get("query") == "" {
		return "", false
	}
	if q.Year > 0 {
		values.Set("year", strconv.Itoa(q.Year))
	}
	if q.Sequence > 0 {
		values.Set("season_number", strconv.Itoa(q.Sequence))
	}
	if q.Index > 0 {
		values.Set("episode_number", strconv.Itoa(q.Index))
	}
	if langs := normalLanguages(q.Languages); len(langs) > 0 {
		values.Set("languages", strings.Join(langs, ","))
	}
	// url.Values.Encode sorts by key.
	return OpenSubtitlesAPIOrigin + "/api/v1/subtitles?" + values.Encode(), true
}

// Search implements Provider.
func (o *OpenSubtitles) Search(ctx context.Context, q Query) ([]Result, error) {
	key, err := o.apiKey()
	if err != nil {
		return nil, err
	}
	target, ok := SearchURL(q)
	if !ok {
		return nil, errors.New("subtitle search needs a file hash or a title")
	}
	resp, err := o.egress.Do(ctx, OpenSubtitlesID, Request{Method: http.MethodGet, URL: target, Header: o.header(key, false)})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &ProviderError{Provider: OpenSubtitlesID, Status: resp.StatusCode}
	}
	var parsed osSearchResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("opensubtitles search: %w", err)
	}
	var out []Result
	for _, item := range parsed.Data {
		a := item.Attributes
		lang := normalLanguage(a.Language)
		if lang == "" {
			continue
		}
		for _, file := range a.Files {
			if file.FileID <= 0 {
				continue
			}
			release := a.Release
			if release == "" {
				release = file.FileName
			}
			out = append(out, Result{
				Provider:  OpenSubtitlesID,
				FileID:    strconv.FormatInt(file.FileID, 10),
				Language:  lang,
				Release:   release,
				Rating:    a.Ratings,
				Downloads: a.DownloadCount,
				HashMatch: a.MoviehashMatch,
				SDH:       a.HearingImpaired,
				Forced:    a.ForeignParts,
			})
		}
	}
	return out, nil
}

// Download implements Provider: ask the API for a link, then fetch it.
func (o *OpenSubtitles) Download(ctx context.Context, fileID string) ([]byte, error) {
	key, err := o.apiKey()
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(fileID, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("invalid subtitle file ID")
	}
	body, err := json.Marshal(map[string]any{"file_id": id, "sub_format": "srt"})
	if err != nil {
		return nil, err
	}
	resp, err := o.egress.Do(ctx, OpenSubtitlesID, Request{
		Method: http.MethodPost,
		URL:    OpenSubtitlesAPIOrigin + "/api/v1/download",
		Header: o.header(key, true),
		Body:   body,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &ProviderError{Provider: OpenSubtitlesID, Status: resp.StatusCode}
	}
	var link struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(resp.Body, &link); err != nil {
		return nil, fmt.Errorf("opensubtitles download: %w", err)
	}
	if !downloadLinkAllowed(link.Link) {
		return nil, errors.New("opensubtitles download link is not on a known origin")
	}
	file, err := o.egress.Do(ctx, OpenSubtitlesID, Request{
		Method: http.MethodGet,
		URL:    link.Link,
		Header: map[string][]string{"User-Agent": {o.userAgent}},
	})
	if err != nil {
		return nil, err
	}
	if file.StatusCode != http.StatusOK {
		return nil, &ProviderError{Provider: OpenSubtitlesID, Status: file.StatusCode}
	}
	return file.Body, nil
}

func downloadLinkAllowed(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return false
	}
	return slices.Contains(OpenSubtitlesDownloadOrigins, parsed.Scheme+"://"+parsed.Host)
}

func validOSHash(hash string) bool {
	if len(hash) != 16 {
		return false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// normalLanguage lowercases a language tag and keeps it only when it is a
// plain letters-and-hyphen tag (2-8 letter primary subtag).
func normalLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(tag, "_", "-")))
	parts := strings.Split(tag, "-")
	if len(parts) > 3 || len(parts[0]) < 2 || len(parts[0]) > 8 {
		return ""
	}
	for i, part := range parts {
		if part == "" || len(part) > 8 {
			return ""
		}
		for _, r := range part {
			if (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
				return ""
			}
		}
	}
	return tag
}

// NormalLanguage is normalLanguage for callers outside the package.
func NormalLanguage(tag string) string { return normalLanguage(tag) }

func normalLanguages(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if lang := normalLanguage(tag); lang != "" && !slices.Contains(out, lang) {
			out = append(out, lang)
		}
	}
	slices.Sort(out)
	return out
}
