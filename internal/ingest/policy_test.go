package ingest

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/store"
)

// addDownloadAllSubscription adds a Subscription with the `everything`
// policy: the download-mechanics tests predate download policies and expect
// the whole feed on disk (the store default, new-only, skips the back
// catalogue).
func addDownloadAllSubscription(ctx context.Context, st *store.Store, subscription store.Subscription, now time.Time) (store.Subscription, error) {
	added, err := st.AddSubscription(ctx, subscription, now)
	if err != nil {
		return added, err
	}
	if err := st.SetSubscriptionDownloadPolicy(ctx, added.ID, store.SubscriptionPolicy{Policy: store.DownloadPolicyEverything}); err != nil {
		return added, err
	}
	return st.Subscription(ctx, added.ID)
}

// advancePollClock moves the poller's clock past the next scheduled poll, so
// a test can poll a healthy feed again right away.
func advancePollClock(p *Poller) {
	at := p.clock().Add(DefaultPollInterval + time.Minute)
	p.now = func() time.Time { return at }
}
