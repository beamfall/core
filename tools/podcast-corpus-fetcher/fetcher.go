package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	manifestVersion = 1
	defaultMaxFeed  = 8 << 20
	defaultMaxAudio = 2 << 30
)

var ErrOptOut = errors.New("source reserves text-and-data-mining rights")

type Request struct {
	FeedURL            string
	EpisodeGUID        string
	OutputRoot         string
	Interval           time.Duration
	AllowedHosts       []string
	PolicyAcknowledged bool
}

type Provenance struct {
	Kind      string `json:"kind"`
	Ordinal   int    `json:"ordinal,omitempty"`
	SourceURL string `json:"source_url"`
	FinalURL  string `json:"final_url"`
	FetchedAt string `json:"fetched_at"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
}

type Manifest struct {
	Version           int          `json:"version"`
	SnapshotSHA256    string       `json:"snapshot_sha256"`
	FeedURL           string       `json:"feed_url"`
	EpisodeGUID       string       `json:"episode_guid"`
	EpisodeTitle      string       `json:"episode_title,omitempty"`
	EnclosureURL      string       `json:"enclosure_url"`
	SingleVantage     bool         `json:"single_vantage"`
	RefetchIntervalNS int64        `json:"refetch_interval_ns"`
	PolicyHosts       []string     `json:"policy_hosts"`
	Items             []Provenance `json:"items"`
}

type manifestPayload struct {
	Version           int          `json:"version"`
	FeedURL           string       `json:"feed_url"`
	EpisodeGUID       string       `json:"episode_guid"`
	EpisodeTitle      string       `json:"episode_title,omitempty"`
	EnclosureURL      string       `json:"enclosure_url"`
	SingleVantage     bool         `json:"single_vantage"`
	RefetchIntervalNS int64        `json:"refetch_interval_ns"`
	PolicyHosts       []string     `json:"policy_hosts"`
	Items             []Provenance `json:"items"`
}

type Result struct {
	Path           string `json:"path"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
}

type Fetcher struct {
	transport http.RoundTripper
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
	maxFeed   int64
	maxAudio  int64
}

func NewFetcher() *Fetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Environment-supplied routing must not turn a single-vantage fetch into a
	// configurable multi-vantage path. The tool always connects directly.
	transport.Proxy = nil
	return &Fetcher{
		transport: transport,
		now:       time.Now,
		sleep:     sleepContext,
		maxFeed:   defaultMaxFeed,
		maxAudio:  defaultMaxAudio,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, req Request) (Result, error) {
	allowed, err := validateRequest(req)
	if err != nil {
		return Result{}, err
	}
	client := &http.Client{
		Transport: f.transport,
		CheckRedirect: func(next *http.Request, _ []*http.Request) error {
			return requireAllowedHost(next.URL, allowed)
		},
	}

	feedBytes, feedMeta, err := f.fetch(ctx, client, req.FeedURL, allowed, f.maxFeed)
	if err != nil {
		return Result{}, fmt.Errorf("fetch feed: %w", err)
	}
	if err := rssOptOut(feedBytes); err != nil {
		return Result{}, err
	}
	episode, err := findEpisode(feedBytes, req.EpisodeGUID)
	if err != nil {
		return Result{}, err
	}
	enclosureURL, err := url.Parse(episode.Enclosure.URL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid enclosure URL: %w", err)
	}
	if err := requireAllowedHost(enclosureURL, allowed); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(req.OutputRoot, 0o700); err != nil {
		return Result{}, fmt.Errorf("create output root: %w", err)
	}
	tmp, err := os.MkdirTemp(req.OutputRoot, ".podcast-snapshot-")
	if err != nil {
		return Result{}, fmt.Errorf("create snapshot staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()

	items := []Provenance{{
		Kind: "feed", SourceURL: req.FeedURL, FinalURL: feedMeta.finalURL,
		FetchedAt: feedMeta.fetchedAt, SHA256: digest(feedBytes), Bytes: int64(len(feedBytes)),
	}}
	if err := writePinned(filepath.Join(tmp, "feed.xml"), feedBytes); err != nil {
		return Result{}, err
	}

	for ordinal := 1; ordinal <= 2; ordinal++ {
		if ordinal == 2 {
			if err := f.sleep(ctx, req.Interval); err != nil {
				return Result{}, err
			}
		}
		name := fmt.Sprintf("audio-%d.bin", ordinal)
		item, err := f.fetchToFile(ctx, client, enclosureURL.String(), allowed, f.maxAudio, filepath.Join(tmp, name))
		if err != nil {
			return Result{}, fmt.Errorf("fetch enclosure sample %d: %w", ordinal, err)
		}
		item.Kind = "audio"
		item.Ordinal = ordinal
		items = append(items, item)
	}

	hosts := append([]string(nil), req.AllowedHosts...)
	for i := range hosts {
		hosts[i] = canonicalHost(hosts[i])
	}
	sort.Strings(hosts)
	payload := manifestPayload{
		Version: manifestVersion, FeedURL: req.FeedURL, EpisodeGUID: req.EpisodeGUID,
		EpisodeTitle: episode.Title, EnclosureURL: enclosureURL.String(), SingleVantage: true,
		RefetchIntervalNS: req.Interval.Nanoseconds(), PolicyHosts: hosts, Items: items,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("encode snapshot payload: %w", err)
	}
	snapshotHash := digest(payloadJSON)
	manifest := Manifest{
		Version: payload.Version, SnapshotSHA256: snapshotHash, FeedURL: payload.FeedURL,
		EpisodeGUID: payload.EpisodeGUID, EpisodeTitle: payload.EpisodeTitle,
		EnclosureURL: payload.EnclosureURL, SingleVantage: payload.SingleVantage,
		RefetchIntervalNS: payload.RefetchIntervalNS, PolicyHosts: payload.PolicyHosts, Items: payload.Items,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encode manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := writePinned(filepath.Join(tmp, "manifest.json"), manifestJSON); err != nil {
		return Result{}, err
	}

	target := filepath.Join(req.OutputRoot, snapshotHash)
	if _, err := os.Stat(target); err == nil {
		return Result{}, fmt.Errorf("snapshot %s already exists; refusing to re-derive or overwrite", snapshotHash)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("inspect snapshot target: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return Result{}, fmt.Errorf("pin snapshot: %w", err)
	}
	if err := os.Chmod(target, 0o500); err != nil { //nolint:gosec // directories require execute permission; write is intentionally absent
		return Result{}, fmt.Errorf("make snapshot immutable: %w", err)
	}
	committed = true
	return Result{Path: target, SnapshotSHA256: snapshotHash}, nil
}

type fetchMeta struct {
	finalURL  string
	fetchedAt string
}

func (f *Fetcher) fetch(ctx context.Context, client *http.Client, rawURL string, allowed map[string]struct{}, maxBytes int64) ([]byte, fetchMeta, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fetchMeta{}, err
	}
	if err := requireAllowedHost(u, allowed); err != nil {
		return nil, fetchMeta{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fetchMeta{}, err
	}
	req.Header.Set("User-Agent", "Beamfall-Podcast-Corpus-Fetcher/1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fetchMeta{}, err
	}
	defer resp.Body.Close()
	if err := requireAllowedHost(resp.Request.URL, allowed); err != nil {
		return nil, fetchMeta{}, err
	}
	if responseOptsOut(resp.Header) {
		return nil, fetchMeta{}, ErrOptOut
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fetchMeta{}, fmt.Errorf("HTTP status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fetchMeta{}, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fetchMeta{}, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}
	return data, fetchMeta{finalURL: resp.Request.URL.String(), fetchedAt: f.now().UTC().Format(time.RFC3339Nano)}, nil
}

func (f *Fetcher) fetchToFile(ctx context.Context, client *http.Client, rawURL string, allowed map[string]struct{}, maxBytes int64, path string) (Provenance, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Provenance{}, err
	}
	if err := requireAllowedHost(u, allowed); err != nil {
		return Provenance{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Provenance{}, err
	}
	req.Header.Set("User-Agent", "Beamfall-Podcast-Corpus-Fetcher/1")
	resp, err := client.Do(req)
	if err != nil {
		return Provenance{}, err
	}
	defer resp.Body.Close()
	if err := requireAllowedHost(resp.Request.URL, allowed); err != nil {
		return Provenance{}, err
	}
	if responseOptsOut(resp.Header) {
		return Provenance{}, ErrOptOut
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Provenance{}, fmt.Errorf("HTTP status %s", resp.Status)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400) //nolint:gosec // path is generated inside the private staging directory
	if err != nil {
		return Provenance{}, fmt.Errorf("create snapshot item: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return Provenance{}, fmt.Errorf("stream snapshot item: %w", err)
	}
	if written > maxBytes {
		return Provenance{}, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}
	if err := file.Close(); err != nil {
		return Provenance{}, fmt.Errorf("close snapshot item: %w", err)
	}
	keep = true
	return Provenance{
		SourceURL: rawURL, FinalURL: resp.Request.URL.String(),
		FetchedAt: f.now().UTC().Format(time.RFC3339Nano),
		SHA256:    hex.EncodeToString(hash.Sum(nil)), Bytes: written,
	}, nil
}

func validateRequest(req Request) (map[string]struct{}, error) {
	if !req.PolicyAcknowledged {
		return nil, errors.New("host-platform policy review must be acknowledged")
	}
	if req.FeedURL == "" || req.EpisodeGUID == "" || req.OutputRoot == "" {
		return nil, errors.New("feed URL, episode GUID, and output root are required")
	}
	if req.Interval <= 0 {
		return nil, errors.New("re-fetch interval must be positive")
	}
	allowed := make(map[string]struct{}, len(req.AllowedHosts))
	for _, host := range req.AllowedHosts {
		host = canonicalHost(host)
		if host != "" {
			allowed[host] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("at least one policy-reviewed host is required")
	}
	u, err := url.Parse(req.FeedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid feed URL: %w", err)
	}
	if err := requireAllowedHost(u, allowed); err != nil {
		return nil, err
	}
	return allowed, nil
}

func requireAllowedHost(u *url.URL, allowed map[string]struct{}) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported source scheme %q", u.Scheme)
	}
	host := canonicalHost(u.Hostname())
	if _, ok := allowed[host]; !ok {
		return fmt.Errorf("host %q lacks an acknowledged platform-policy permit", host)
	}
	return nil
}

func canonicalHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func responseOptsOut(header http.Header) bool {
	if reservedValue(header.Get("TDM-Reservation")) {
		return true
	}
	for _, value := range header.Values("X-Robots-Tag") {
		lower := strings.ToLower(value)
		if strings.Contains(lower, "noai") {
			return true
		}
	}
	return false
}

func rssOptOut(data []byte) error {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse RSS opt-out signals: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		name := normalizedName(start.Name.Local)
		candidate := name == "tdmreservation" || name == "textanddatamining" || name == "textdatamining"
		if name == "reservation" && strings.Contains(strings.ToLower(start.Name.Space), "tdm") {
			candidate = true
		}
		if name == "txt" {
			for _, attr := range start.Attr {
				if normalizedName(attr.Name.Local) == "purpose" && strings.Contains(normalizedName(attr.Value), "tdm") {
					candidate = true
				}
			}
		}
		if !candidate {
			continue
		}
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return fmt.Errorf("parse RSS opt-out value: %w", err)
		}
		if reservedValue(value) {
			return ErrOptOut
		}
	}
}

func normalizedName(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("-", "", "_", "", " ", "", ":", "").Replace(value)
}

func reservedValue(value string) bool {
	switch normalizedName(strings.TrimSpace(value)) {
	case "1", "true", "yes", "reserved", "optout", "deny", "disallow":
		return true
	default:
		return false
	}
}

type rssDocument struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	GUID      string `xml:"guid"`
	Title     string `xml:"title"`
	Enclosure struct {
		URL string `xml:"url,attr"`
	} `xml:"enclosure"`
}

func findEpisode(data []byte, guid string) (rssItem, error) {
	var rss rssDocument
	if err := xml.Unmarshal(data, &rss); err != nil {
		return rssItem{}, fmt.Errorf("parse RSS feed: %w", err)
	}
	for _, item := range rss.Channel.Items {
		if strings.TrimSpace(item.GUID) == guid {
			if item.Enclosure.URL == "" {
				return rssItem{}, fmt.Errorf("episode %q has no enclosure", guid)
			}
			return item, nil
		}
	}
	return rssItem{}, fmt.Errorf("episode GUID %q not found", guid)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writePinned(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o400); err != nil {
		return fmt.Errorf("write snapshot item: %w", err)
	}
	return nil
}

func sleepContext(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func verifyManifestHash(manifest Manifest) (string, error) {
	payload := manifestPayload{
		Version: manifest.Version, FeedURL: manifest.FeedURL, EpisodeGUID: manifest.EpisodeGUID,
		EpisodeTitle: manifest.EpisodeTitle, EnclosureURL: manifest.EnclosureURL,
		SingleVantage: manifest.SingleVantage, RefetchIntervalNS: manifest.RefetchIntervalNS,
		PolicyHosts: manifest.PolicyHosts, Items: manifest.Items,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}
