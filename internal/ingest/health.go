package ingest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// Feed health policy (ADR-0238 §2.2, CORE-2955).
const (
	// MinPollInterval and MaxPollInterval clamp every Subscription's poll
	// interval. The poll job runs at MinPollInterval and polls only the
	// Subscriptions that are due.
	MinPollInterval = 15 * time.Minute
	MaxPollInterval = 24 * time.Hour
	// failureBackoffBase is the delay after the first failure; each further
	// consecutive failure doubles it, up to MaxPollInterval, so a feed that
	// has failed for a month still polls daily.
	failureBackoffBase = 15 * time.Minute
	// backoffJitter spreads retries by ±20% so feeds sharing a host do not
	// retry in lockstep.
	backoffJitter = 0.2
)

// clampPollInterval bounds a poll interval to [MinPollInterval, MaxPollInterval].
func clampPollInterval(interval time.Duration) time.Duration {
	return min(max(interval, MinPollInterval), MaxPollInterval)
}

// failureBackoff is the delay before the next poll after the failures-th
// consecutive failure. jitter is a uniform sample in [0, 1).
func failureBackoff(failures int, jitter float64) time.Duration {
	delay := MaxPollInterval
	if failures < 1 {
		failures = 1
	}
	if shift := failures - 1; shift < 16 {
		delay = min(failureBackoffBase<<shift, MaxPollInterval)
	}
	scaled := time.Duration(float64(delay) * (1 - backoffJitter + 2*backoffJitter*jitter))
	return min(max(scaled, time.Minute), MaxPollInterval)
}

// classifyFeedError maps a feed fetch failure to its closed error class.
func classifyFeedError(err error) store.FeedErrorClass {
	if status, ok := scrape.HTTPStatus(err); ok {
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return store.FeedErrorAuth
		case status >= 500:
			return store.FeedErrorHTTP5xx
		default:
			return store.FeedErrorHTTP4xx
		}
	}
	if scrape.IsRefused(err) {
		return store.FeedErrorSSRFBlocked
	}
	if errors.Is(err, scrape.ErrResponseTooLarge) {
		return store.FeedErrorParse
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return store.FeedErrorDNS
	}
	if isTLSError(err) {
		return store.FeedErrorTLS
	}
	return store.FeedErrorNetwork
}

func isTLSError(err error) bool {
	var (
		unknownAuthority x509.UnknownAuthorityError
		hostname         x509.HostnameError
		invalid          x509.CertificateInvalidError
		verification     *tls.CertificateVerificationError
		record           tls.RecordHeaderError
		alert            tls.AlertError
	)
	switch {
	case errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid),
		errors.As(err, &verification), errors.As(err, &record), errors.As(err, &alert):
		return true
	}
	return strings.Contains(err.Error(), "tls: ") || strings.Contains(err.Error(), "x509: ")
}

// recordFeedFailure records one failed poll with its backed-off next poll. A
// 410 Gone retires the feed instead. The poll error is returned, joined with
// any error recording it.
func (p *Poller) recordFeedFailure(ctx context.Context, subscription store.Subscription, class store.FeedErrorClass, cause error, now time.Time) error {
	var err error
	if status, ok := scrape.HTTPStatus(cause); ok && status == http.StatusGone {
		err = p.store.RecordSubscriptionRetired(ctx, subscription.ID, now)
	} else {
		next := now.Add(failureBackoff(subscription.Health.ConsecutiveFailures+1, p.jitter()))
		err = p.store.RecordSubscriptionPollFailure(ctx, subscription.ID, class, now, next)
	}
	return errors.Join(cause, err)
}

// markPolled stores the poll's validators and records a healthy feed due
// again after the clamped poll interval.
func (p *Poller) markPolled(ctx context.Context, subscription store.Subscription, etag, lastModified string, now time.Time) error {
	if err := p.store.MarkSubscriptionPolled(ctx, subscription.ID, etag, lastModified, now); err != nil {
		return err
	}
	return p.store.RecordSubscriptionPollSuccess(ctx, subscription.ID, now.Add(clampPollInterval(DefaultPollInterval)))
}

// observeMove records where a successful fetch says the feed now lives: a
// chain of permanent redirects, else a new-feed-URL declaration in the
// document. A 304 with no redirect says nothing and leaves any pending move
// as it was. A candidate the SSRF rules refuse is ignored, and so is one on
// another origin when the feed carries a credential, so a move never sends
// the credential to a new host.
func (p *Poller) observeMove(ctx context.Context, subscription store.Subscription, result scrape.FeedResult, now time.Time) error {
	candidate := result.PermanentURL
	if candidate == "" {
		if result.NotModified {
			return nil
		}
		candidate = scrape.FeedNewURL(result.Data)
	}
	if candidate != "" && !p.engine.PermitsURL(candidate) {
		candidate = ""
	}
	if candidate != "" && subscription.AuthRef != "" && !scrape.SameOrigin(candidate, subscription.FeedURL) {
		candidate = ""
	}
	_, err := p.store.RecordSubscriptionMoveObservation(ctx, subscription.ID, candidate, now)
	return err
}
