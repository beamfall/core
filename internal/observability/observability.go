package observability

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const DefaultRingSize = 256

var ErrSensitiveAttribute = errors.New("observability attribute violates ADR-0146 decision 9")

type Sample struct {
	At     time.Time
	Name   string
	Value  int64
	Labels map[string]string
}

type Controller struct {
	mu       sync.Mutex
	enabled  bool
	size     int
	ring     []Sample
	next     int
	provider *sdkmetric.MeterProvider
	handler  http.Handler
	gauges   map[string]otelmetric.Int64Gauge
}

func New(size int) *Controller {
	if size < 1 {
		size = DefaultRingSize
	}
	return &Controller{size: size}
}

func (c *Controller) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

func (c *Controller) SetEnabled(ctx context.Context, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled == enabled {
		return nil
	}
	if !enabled {
		if c.provider != nil {
			_ = c.provider.Shutdown(ctx)
		}
		c.enabled = false
		c.provider = nil
		c.handler = nil
		c.gauges = nil
		c.ring = nil
		c.next = 0
		return nil
	}
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry), otelprom.WithoutTargetInfo(), otelprom.WithoutScopeInfo())
	if err != nil {
		return err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	c.enabled = true
	c.provider = provider
	c.handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	c.gauges = map[string]otelmetric.Int64Gauge{}
	return nil
}

func (c *Controller) RecordGauge(ctx context.Context, name string, value int64, labels map[string]string) error {
	if err := validateLabels(labels); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return nil
	}
	gauge, ok := c.gauges[name]
	if !ok {
		var err error
		gauge, err = c.provider.Meter("github.com/beamfall/core").Int64Gauge(name)
		if err != nil {
			return err
		}
		c.gauges[name] = gauge
	}
	attrs := make([]otelmetric.RecordOption, 0, 1)
	if len(labels) > 0 {
		pairs := make([]attribute.KeyValue, 0, len(labels))
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			pairs = append(pairs, attribute.String(key, labels[key]))
		}
		attrs = append(attrs, otelmetric.WithAttributes(pairs...))
	}
	gauge.Record(ctx, value, attrs...)
	c.appendLocked(Sample{At: time.Now().UTC(), Name: name, Value: value, Labels: cloneLabels(labels)})
	return nil
}

func (c *Controller) PrometheusText(r *http.Request) string {
	c.mu.Lock()
	handler := c.handler
	c.mu.Unlock()
	if handler == nil {
		return ""
	}
	rec := &captureHeader{header: http.Header{}}
	handler.ServeHTTP(rec, r)
	return rec.body.String()
}

func (c *Controller) Snapshot() []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ring) < c.size || c.next == 0 {
		return append([]Sample(nil), c.ring...)
	}
	out := append([]Sample(nil), c.ring[c.next:]...)
	out = append(out, c.ring[:c.next]...)
	return out
}

func (c *Controller) appendLocked(sample Sample) {
	if len(c.ring) < c.size {
		c.ring = append(c.ring, sample)
		return
	}
	c.ring[c.next] = sample
	c.next = (c.next + 1) % c.size
}

type captureHeader struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (c *captureHeader) Header() http.Header { return c.header }

func (c *captureHeader) WriteHeader(status int) { c.status = status }

func (c *captureHeader) Write(data []byte) (int, error) { return c.body.Write(data) }

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

func validateLabels(labels map[string]string) error {
	for key, value := range labels {
		if ForbiddenLabelKey(key) || ForbiddenLabelValue(value) {
			return fmt.Errorf("%w: %s", ErrSensitiveAttribute, key)
		}
	}
	return nil
}

// ForbiddenLabelKey reports whether a metric label / span attribute key names a
// Decision-9 sensitive dimension (media title, filesystem path, library /
// profile / account identifier, token, or client IP). Exported so the
// conformance static scan (ADR-0146 Consequences (b)) enforces the same denylist
// over every instrumentation call site that RecordGauge enforces at runtime —
// one source of truth, no drift.
func ForbiddenLabelKey(key string) bool { return sensitiveLabelKey(key) }

// ForbiddenLabelValue reports whether a literal label / attribute value carries
// a Decision-9 secret (bearer/basic credential, key prefix, media/pack path, or
// token query parameter). The static scan applies it to string-literal values;
// dynamic values are backstopped by validateLabels at runtime.
func ForbiddenLabelValue(value string) bool { return sensitiveLabelValue(value) }

func sensitiveLabelKey(key string) bool {
	k := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	if strings.Contains(k, "title") || strings.Contains(k, "path") || strings.Contains(k, "token") {
		return true
	}
	if strings.Contains(k, "clientip") {
		return true
	}
	return strings.Contains(k, "library"+"id") ||
		strings.Contains(k, "profile"+"id") ||
		strings.Contains(k, "account"+"id")
}

func sensitiveLabelValue(value string) bool {
	v := strings.ToLower(value)
	return strings.Contains(v, "bearer ") ||
		strings.Contains(v, "basic ") ||
		strings.Contains(v, "bfk_") ||
		strings.Contains(v, "/api/media/") ||
		strings.Contains(v, "/api/pack/") ||
		strings.Contains(v, "?token=") ||
		strings.Contains(v, "&token=")
}
