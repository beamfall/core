// Package apptest is the in-process test harness for the Beamfall Core app: the
// app-layer sibling of internal/store/storetest. Boot(t) stands up a fresh,
// fully wired *app.App over app.New + net/http/httptest and registers cleanup,
// so test call sites stay flat:
//
//	h := apptest.Boot(t)
//	resp := h.Client.GET("/api/manifest")
//	h.Assert.OK(resp)
//
// Seeding is per-test by default (each Boot gets its own temp store); there is
// deliberately no shared global world. BootSeeded selects the deterministic
// conformance corpus. Options mutate config or swap plugins before app.New.
package apptest

import (
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/apptest/synthplugin"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// Harness is the booted app under test, with layered sub-helpers wired in Boot.
type Harness struct {
	// App is the live in-process application.
	App *app.App
	// Server is the loopback httptest server fronting App.Handler().
	Server *httptest.Server
	// Client issues requests against Server (GET/POST/GraphQL).
	Client *Client
	// Assert holds response assertions that t.Fatalf on mismatch.
	Assert *Assert
	// Fixtures accesses the generated media fixtures + manifest.
	Fixtures *Fixtures
}

// options collects functional-option state applied before app.New.
type options struct {
	plugins   []plugin.Bundle
	configFns []func(*config.Config)
	logger    *slog.Logger
}

// Option configures a Boot/BootSeeded call.
type Option func(*options)

// WithPlugins replaces the default synthplugin bundle with the given bundles.
func WithPlugins(bundles ...plugin.Bundle) Option {
	return func(o *options) { o.plugins = bundles }
}

// WithConfig registers a config mutator run after the base config is built and
// before app.New. Multiple WithConfig options apply in order.
func WithConfig(fn func(*config.Config)) Option {
	return func(o *options) { o.configFns = append(o.configFns, fn) }
}

// WithLogger sets the app logger (nil — the default — discards via app.New).
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// Boot stands up a development-seeded (empty corpus) app and returns a Harness
// whose resources are released at test end.
func Boot(t testing.TB, opts ...Option) *Harness {
	t.Helper()
	return boot(t, false, opts...)
}

// BootSeeded stands up a conformance-seeded (deterministic fixture corpus) app.
// app.New pins the conformance read-clock + stream-token key for this world, so
// its wire output is byte-stable. Per-call: no shared global store.
func BootSeeded(t testing.TB, opts ...Option) *Harness {
	t.Helper()
	return boot(t, true, opts...)
}

func boot(t testing.TB, conformance bool, opts ...Option) *Harness {
	t.Helper()

	o := options{plugins: []plugin.Bundle{synthplugin.Bundle()}}
	for _, opt := range opts {
		opt(&o)
	}

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.StreamTokenKey = []byte("12345678901234567890123456789012")
	if conformance {
		cfg.SeedProfile = string(store.SeedProfileConformance)
	} else {
		cfg.SeedDevelopmentData = true
	}
	for _, fn := range o.configFns {
		fn(&cfg)
	}

	beamfall, err := app.New(t.Context(), cfg, o.logger, o.plugins)
	if err != nil {
		t.Fatalf("apptest.Boot: app.New: %v", err)
	}
	t.Cleanup(func() { _ = beamfall.Close() })

	server := httptest.NewServer(beamfall.Handler())
	t.Cleanup(server.Close)

	h := &Harness{App: beamfall, Server: server}
	h.Client = &Client{t: t, base: server.URL, client: server.Client()}
	h.Assert = &Assert{t: t}
	h.Fixtures = &Fixtures{t: t}
	return h
}
