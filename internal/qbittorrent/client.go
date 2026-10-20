// Package qbittorrent ingests completed downloads from a qBittorrent instance
// into Beamfall Libraries. It speaks the qBittorrent Web API
// directly for completion and category truth, routes by an explicit
// operator-declared category->Library map (never inference — a misroute is a
// Reveal-contract incident, ADR-0020 §3), hardlinks the payload into the target
// Library's Incoming Folder (copy fallback across filesystems, never move,
// deletes nothing — §2), skips exact-fingerprint duplicates (§5), attaches a
// Parse Hints sidecar, and hands off to the standard scan pipeline (§4). The
// plugin contains zero identification logic.
//
// The Web API client sits behind the source-adapter Source seam (source.go) so
// later adapters (watch-folder, *arr coordination — §1) land without redesign.
//
// Credentials are secrets (invariant 5): nothing in this package logs the
// endpoint with credentials, the password, or the session cookie.
package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTimeout bounds every Web API request. qBittorrent is a local service;
// a slow response means it is wedged, not that the body is large.
const defaultTimeout = 30 * time.Second

// maxResponseBytes caps any single API response body. Category and torrent
// listings are JSON metadata, never media-sized; 32 MiB tolerates a very large
// catalog while bounding a hostile or wedged endpoint.
const maxResponseBytes int64 = 32 << 20

// ErrAuthRequired is returned when the endpoint rejects the session and a
// re-login did not recover it (bad credentials, or auth disabled differently
// than expected).
var ErrAuthRequired = errors.New("qbittorrent: authentication required")

// Credentials carries the operator-configured connection settings. Username
// and Password are resolved from the secure store by the composition root and
// passed in here — this package never reads a secret file itself.
type Credentials struct {
	// BaseURL is the qBittorrent Web API base, e.g. "http://localhost:8080".
	BaseURL string
	// Username and Password authenticate the Web API session. Both empty is
	// valid: qBittorrent can be configured to skip auth for some hosts.
	Username string
	Password string
}

// Category is a qBittorrent category and its declared save path.
type Category struct {
	Name     string `json:"name"`
	SavePath string `json:"savePath"`
}

// Torrent is the subset of a qBittorrent torrent the ingest pipeline needs.
type Torrent struct {
	// Hash is the torrent's info-hash: the stable idempotency key (§5).
	Hash string `json:"hash"`
	// Name is the release name — usually far more parseable than the payload
	// filenames, so it becomes the primary Parse Hint (§4).
	Name string `json:"name"`
	// Category is the routing key (§3); empty means uncategorized.
	Category string `json:"category"`
	// ContentPath is the absolute path qBittorrent saved the payload to: a file
	// for single-file torrents, a directory for multi-file torrents.
	ContentPath string `json:"content_path"`
	// SavePath is the torrent's save directory.
	SavePath string `json:"save_path"`
	// Progress is 0..1; 1 means the download completed.
	Progress float64 `json:"progress"`
	// State is qBittorrent's torrent state string (e.g. "stalledUP").
	State string `json:"state"`
}

// TorrentFile is one file inside a (possibly multi-file) torrent.
type TorrentFile struct {
	// Name is the file path relative to the torrent's content root.
	Name string `json:"name"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
}

// Client is a thin qBittorrent Web API v2 client. It is safe for sequential
// use by one ingest job; it is not designed for concurrent callers (the ingest
// job claims one run at a time).
type Client struct {
	creds      Credentials
	httpClient *http.Client
	loggedIn   bool
}

// NewClient builds a Client with a hardened HTTP client: bounded timeouts, no
// environment proxy, and a cookie jar scoped to this client so the session
// cookie never leaks to other traffic. transport may be nil for the default.
func NewClient(creds Credentials) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(creds.BaseURL), "/")
	if base == "" {
		return nil, errors.New("qbittorrent: base URL is required")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: invalid base URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("qbittorrent: base URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("qbittorrent: base URL must include a host")
	}
	creds.BaseURL = base
	return &Client{
		creds:      creds,
		httpClient: newHardenedHTTPClient(),
	}, nil
}

// SetHTTPClient overrides the HTTP client (tests inject an httptest-backed
// client). A nil client restores the hardened default.
func (c *Client) SetHTTPClient(client *http.Client) {
	if client == nil {
		client = newHardenedHTTPClient()
	}
	c.httpClient = client
	c.loggedIn = false
}

// newHardenedHTTPClient mirrors the repo's hardened-client posture (ADR-0012
// §9) for an operator-configured local service: no environment proxy, bounded
// dial/handshake/header timeouts, and a per-client cookie jar. Unlike the
// scrape engine, it does NOT forbid private IPs or http — a qBittorrent
// endpoint is the operator's own LAN/localhost service that they deliberately
// pointed at, not an attacker-influenced scrape target.
func newHardenedHTTPClient() *http.Client {
	jar := newCookieJar()
	transport := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   defaultTimeout,
		ResponseHeaderTimeout: defaultTimeout,
	}
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
		Jar:       jar,
		// Refuse redirects: the Web API never legitimately redirects, and a
		// redirect could exfiltrate the session cookie to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Login authenticates the Web API session. It is idempotent: a successful login
// sets a session cookie in the jar; subsequent calls re-issue it. An empty
// username AND password skips login (qBittorrent host-bypass auth).
func (c *Client) Login(ctx context.Context) error {
	if c.creds.Username == "" && c.creds.Password == "" {
		c.loggedIn = true
		return nil
	}
	form := url.Values{}
	form.Set("username", c.creds.Username)
	form.Set("password", c.creds.Password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.creds.BaseURL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// qBittorrent requires a Referer matching the host or it returns 403.
	req.Header.Set("Referer", c.creds.BaseURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The error may embed the base URL but never the credentials; still,
		// keep it generic so callers never log a URL+port that aids scanning.
		return errors.New("qbittorrent: login request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: too many failed login attempts (banned)", ErrAuthRequired)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent: login returned status %d", resp.StatusCode)
	}
	// qBittorrent returns the literal "Fails." on bad credentials with 200.
	if strings.TrimSpace(string(body)) == "Fails." {
		return fmt.Errorf("%w: invalid credentials", ErrAuthRequired)
	}
	c.loggedIn = true
	return nil
}

// Categories returns the configured qBittorrent categories.
func (c *Client) Categories(ctx context.Context) ([]Category, error) {
	var raw map[string]Category
	if err := c.getJSON(ctx, "/api/v2/torrents/categories", nil, &raw); err != nil {
		return nil, err
	}
	categories := make([]Category, 0, len(raw))
	for name, category := range raw {
		// qBittorrent keys the map by name; the value's Name can be blank.
		category.Name = name
		categories = append(categories, category)
	}
	return categories, nil
}

// CompletedTorrents returns torrents qBittorrent reports as completed (filter
// "completed": fully downloaded, regardless of seeding state). Seeding is
// undisturbed by ingest, so completed-and-seeding torrents are included.
func (c *Client) CompletedTorrents(ctx context.Context) ([]Torrent, error) {
	var torrents []Torrent
	query := url.Values{"filter": {"completed"}}
	if err := c.getJSON(ctx, "/api/v2/torrents/info", query, &torrents); err != nil {
		return nil, err
	}
	return torrents, nil
}

// DefaultSavePath returns qBittorrent's global default save path — the
// directory completed downloads land in when their category declares no save
// path of its own. It rounds out the endpoint's category save paths as the
// canonical approved download roots captured at configuration time.
func (c *Client) DefaultSavePath(ctx context.Context) (string, error) {
	body, err := c.getAuthed(ctx, "/api/v2/app/defaultSavePath", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// TorrentFiles lists the files inside one torrent, by info-hash.
func (c *Client) TorrentFiles(ctx context.Context, hash string) ([]TorrentFile, error) {
	if strings.TrimSpace(hash) == "" {
		return nil, errors.New("qbittorrent: torrent hash is required")
	}
	var files []TorrentFile
	query := url.Values{"hash": {hash}}
	if err := c.getJSON(ctx, "/api/v2/torrents/files", query, &files); err != nil {
		return nil, err
	}
	return files, nil
}

// getJSON performs an authenticated GET and decodes a capped JSON body into
// out.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.getAuthed(ctx, path, query)
	if err != nil {
		return err
	}
	return decodeJSON(body, out)
}

// getAuthed performs a GET, re-logging in once on a 403 (the session may have
// expired), and returns the capped raw body of a 200 response.
func (c *Client) getAuthed(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if !c.loggedIn {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}
	status, body, err := c.get(ctx, path, query)
	if err != nil {
		return nil, err
	}
	if status == http.StatusForbidden {
		// Session expired: log in again and retry once.
		c.loggedIn = false
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		status, body, err = c.get(ctx, path, query)
		if err != nil {
			return nil, err
		}
	}
	if status == http.StatusForbidden {
		return nil, ErrAuthRequired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("qbittorrent: %s returned status %d", path, status)
	}
	return body, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values) (int, []byte, error) {
	endpoint := c.creds.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Referer", c.creds.BaseURL)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("qbittorrent: request to %s failed", path)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
