// Package subtitlesearch finds subtitle files at online providers and fetches
// them (CORE-1690). It is domain-agnostic: a query names a file by its
// OpenSubtitles hash and size and by generic Media fields (title, year and the
// stored sequence and index ordinals), never by a domain concept.
//
// The package never builds an HTTP client. Every request goes through an
// injected Egress, which production wires onto the hardened outbound proxy, so
// tests run against recorded fixtures and a server with search disabled cannot
// reach a provider from here.
package subtitlesearch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Request is one outbound call a provider asks the egress seam to make.
type Request struct {
	Method string
	URL    string
	Header map[string][]string
	Body   []byte
}

// Response is the egress seam's answer, body already bounded by the seam.
type Response struct {
	StatusCode int
	Body       []byte
}

// Egress carries a provider's requests. providerID names the provider so the
// seam can apply that provider's outbound policy.
type Egress interface {
	Do(ctx context.Context, providerID string, req Request) (Response, error)
}

// Query names the file to find subtitles for. Every field but OSHash or Title
// is optional; a provider sends what it has.
type Query struct {
	// OSHash is the 16-hex OpenSubtitles hash of the file, "" when unknown.
	OSHash string
	// ByteSize is the file's size in bytes, 0 when unknown.
	ByteSize int64
	Title    string
	Year     int
	// Sequence and Index are the Media's stored structural ordinals, 0 when
	// the Library plugin stores none.
	Sequence int
	Index    int
	// Languages are lowercase language tags such as "en" or "pt-br".
	Languages []string
}

// Result is one downloadable subtitle file a provider offered.
type Result struct {
	Provider  string  `json:"provider"`
	FileID    string  `json:"fileId"`
	Language  string  `json:"language"`
	Release   string  `json:"release"`
	Rating    float64 `json:"rating"`
	Downloads int     `json:"downloads"`
	HashMatch bool    `json:"hashMatch"`
	SDH       bool    `json:"sdh"`
	Forced    bool    `json:"forced"`
}

// Provider is one online subtitle source.
type Provider interface {
	// ID is the provider's stable slug, also its outbound policy ID.
	ID() string
	// Ready reports whether the provider can make requests (it has a key).
	Ready(ctx context.Context) bool
	Search(ctx context.Context, q Query) ([]Result, error)
	// Download returns the subtitle file's bytes as SRT text.
	Download(ctx context.Context, fileID string) ([]byte, error)
}

var (
	// ErrDisabled reports that no provider (or not the named one) is enabled.
	ErrDisabled = errors.New("subtitle search is not enabled")
	// ErrNoKey reports that the provider has no API key to send.
	ErrNoKey = errors.New("subtitle provider has no API key")
	// ErrNotSubtitle reports downloaded bytes that are not SRT text.
	ErrNotSubtitle = errors.New("downloaded file is not an SRT subtitle")
)

// ProviderError is a provider answering with a non-success status.
type ProviderError struct {
	Provider string
	Status   int
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("subtitle provider %s answered %d", e.Provider, e.Status)
}

// ProviderStatus is one enabled provider's readiness.
type ProviderStatus struct {
	ID    string `json:"id"`
	Ready bool   `json:"ready"`
}

// Service searches every enabled provider. A nil or empty Service is search
// disabled and makes no request.
type Service struct {
	providers []Provider
}

// NewService returns a Service over the enabled providers, in order.
func NewService(providers ...Provider) *Service {
	return &Service{providers: slices.Clone(providers)}
}

// Enabled reports whether any provider is enabled.
func (s *Service) Enabled() bool { return s != nil && len(s.providers) > 0 }

// Status lists the enabled providers and whether each can make requests.
func (s *Service) Status(ctx context.Context) []ProviderStatus {
	if s == nil {
		return nil
	}
	out := make([]ProviderStatus, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, ProviderStatus{ID: p.ID(), Ready: p.Ready(ctx)})
	}
	return out
}

// Search queries each ready provider and returns their results, hash matches
// first, then by download count. A provider error fails the search only when
// no provider returned results.
func (s *Service) Search(ctx context.Context, q Query) ([]Result, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	var results []Result
	var firstErr error
	asked := false
	for _, p := range s.providers {
		if !p.Ready(ctx) {
			continue
		}
		asked = true
		found, err := p.Search(ctx, q)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		results = append(results, found...)
	}
	if !asked {
		return nil, ErrNoKey
	}
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	slices.SortStableFunc(results, func(a, b Result) int {
		if a.HashMatch != b.HashMatch {
			if a.HashMatch {
				return -1
			}
			return 1
		}
		return b.Downloads - a.Downloads
	})
	return results, nil
}

// Download fetches one file from the named provider and checks it is SRT text.
func (s *Service) Download(ctx context.Context, providerID, fileID string) ([]byte, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	for _, p := range s.providers {
		if p.ID() != providerID {
			continue
		}
		if !p.Ready(ctx) {
			return nil, ErrNoKey
		}
		body, err := p.Download(ctx, fileID)
		if err != nil {
			return nil, err
		}
		if !LooksLikeSRT(body) {
			return nil, ErrNotSubtitle
		}
		return body, nil
	}
	return nil, ErrDisabled
}

// LooksLikeSRT reports whether body is UTF-8 text with at least one SRT cue
// timing line and no NUL bytes.
func LooksLikeSRT(body []byte) bool {
	if len(body) == 0 || slices.Contains(body, 0) {
		return false
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	if !utf8.ValidString(text) {
		return false
	}
	for line := range strings.Lines(text) {
		if srtTiming(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// srtTiming matches "00:00:01,000 --> 00:00:02,500" loosely: two
// h:mm:ss[,.]mmm stamps around an arrow.
func srtTiming(line string) bool {
	start, end, ok := strings.Cut(line, "-->")
	if !ok {
		return false
	}
	return srtStamp(strings.TrimSpace(start)) && srtStamp(firstField(end))
}

func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func srtStamp(s string) bool {
	clock, millis, ok := strings.Cut(strings.ReplaceAll(s, ".", ","), ",")
	if !ok || len(millis) == 0 || len(millis) > 3 || !digits(millis) {
		return false
	}
	parts := strings.Split(clock, ":")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 2 || !digits(part) {
			return false
		}
	}
	return true
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
