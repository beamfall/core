package backup

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultS3HTTPTimeout = 30 * time.Second

var defaultS3HTTPClient = &http.Client{Timeout: defaultS3HTTPTimeout}

// S3Bucket is a minimal S3-compatible BucketClient for the BYO-bucket path.
// It uses only the S3 REST API (PUT, GET, DELETE, LIST via
// ListObjectsV2) so any S3-compatible service — AWS S3, Backblaze B2,
// Cloudflare R2, MinIO — can be used by setting Endpoint, Bucket, and a
// signing function. No AWS SDK dependency enters the AGPL core.
//
// This implementation is intentionally minimal: it satisfies the BucketClient
// interface over plain HTTP with a caller-supplied auth hook (RequestSigner)
// so operators can wire in AWS Sig4, Bearer tokens, HMAC, or any custom auth
// without the backup package knowing anything about credentials (invariant #5).
type S3Bucket struct {
	// Endpoint is the base URL of the S3-compatible service, e.g.
	// "https://s3.us-east-1.amazonaws.com" or "http://localhost:9000".
	// No trailing slash.
	Endpoint string

	// Bucket is the bucket name.
	Bucket string

	// RequestSigner, if non-nil, is called before each request so the caller
	// can add Authorization, x-amz-*, or other headers. A nil signer sends
	// unauthenticated requests (useful for local MinIO with no-auth mode or
	// httptest stubs).
	RequestSigner func(r *http.Request) error

	// Client is the HTTP client to use. If nil, a bounded package client is used.
	Client *http.Client
}

// ErrNotFound is returned by Get when the key does not exist (HTTP 404).
var ErrNotFound = errors.New("backup/s3: object not found")

func (s *S3Bucket) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return defaultS3HTTPClient
}

func (s *S3Bucket) objectURL(key string) string {
	return strings.TrimRight(s.Endpoint, "/") + "/" + url.PathEscape(s.Bucket) + "/" + escapeS3KeyPath(key)
}

func (s *S3Bucket) sign(r *http.Request) error {
	if s.RequestSigner != nil {
		return s.RequestSigner(r)
	}
	return nil
}

// Put streams r to the bucket under key using HTTP PUT.
func (s *S3Bucket) Put(ctx context.Context, key string, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), r)
	if err != nil {
		return fmt.Errorf("backup/s3: build PUT request: %w", err)
	}
	if err := s.sign(req); err != nil {
		return fmt.Errorf("backup/s3: sign PUT request: %w", err)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("backup/s3: PUT %q: %w", key, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("backup/s3: PUT %q: unexpected status %d", key, resp.StatusCode)
	}
	return nil
}

// Get fetches the object at key. The caller must close the returned ReadCloser.
// A 404 response returns ErrNotFound.
func (s *S3Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, fmt.Errorf("backup/s3: build GET request: %w", err)
	}
	if err := s.sign(req); err != nil {
		return nil, fmt.Errorf("backup/s3: sign GET request: %w", err)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("backup/s3: GET %q: %w", key, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("backup/s3: GET %q: unexpected status %d", key, resp.StatusCode)
	}
	return resp.Body, nil
}

// Delete removes the object at key using HTTP DELETE. A 404 is not an error.
func (s *S3Bucket) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return fmt.Errorf("backup/s3: build DELETE request: %w", err)
	}
	if err := s.sign(req); err != nil {
		return fmt.Errorf("backup/s3: sign DELETE request: %w", err)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("backup/s3: DELETE %q: %w", key, err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("backup/s3: DELETE %q: unexpected status %d", key, resp.StatusCode)
}

// List returns object keys starting with prefix using S3 ListObjectsV2 XML.
func (s *S3Bucket) List(ctx context.Context, prefix string) ([]string, error) {
	values := url.Values{}
	values.Set("list-type", "2")
	values.Set("prefix", prefix)

	var keys []string
	seenTokens := map[string]bool{}
	for {
		listURL := strings.TrimRight(s.Endpoint, "/") + "/" + url.PathEscape(s.Bucket) + "/?" + values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return nil, fmt.Errorf("backup/s3: build LIST request: %w", err)
		}
		if err := s.sign(req); err != nil {
			return nil, fmt.Errorf("backup/s3: sign LIST request: %w", err)
		}
		resp, err := s.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("backup/s3: LIST %q: %w", prefix, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("backup/s3: LIST %q: unexpected status %d", prefix, resp.StatusCode)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("backup/s3: LIST %q: read body: %w", prefix, readErr)
		}
		var parsed s3ListObjectsV2Result
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("backup/s3: LIST %q: parse XML: %w", prefix, err)
		}
		keys = append(keys, parsed.Keys()...)
		if !parsed.IsTruncated {
			return keys, nil
		}
		if parsed.NextContinuationToken == "" {
			return nil, fmt.Errorf("backup/s3: LIST %q: truncated response missing continuation token", prefix)
		}
		if seenTokens[parsed.NextContinuationToken] {
			return nil, fmt.Errorf("backup/s3: LIST %q: repeated continuation token", prefix)
		}
		seenTokens[parsed.NextContinuationToken] = true
		values.Set("continuation-token", parsed.NextContinuationToken)
	}
}

type s3ListObjectsV2Result struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
}

func (r s3ListObjectsV2Result) Keys() []string {
	keys := make([]string, 0, len(r.Contents))
	for _, content := range r.Contents {
		if content.Key != "" {
			keys = append(keys, content.Key)
		}
	}
	return keys
}

func escapeS3KeyPath(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
