package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/beamfall/core/internal/jsonstate"
)

// SecretStore reads and writes per-endpoint secrets. The composition root
// supplies the 0600 secret-file store; SetSecret with an empty value removes
// the secret.
type SecretStore interface {
	SecretSource
	SetSecret(ref, value string) error
}

// ManagerOptions wires a console-managed emitter.
type ManagerOptions struct {
	// ConfigPath is the endpoint file (DataDir/webhook-endpoints.json).
	ConfigPath string
	// StatePath holds the cursors, dead-letter and delivery log
	// (DataDir/webhook-dead-letter.json).
	StatePath string
	Secrets   SecretStore
	Events    EventStore
	// Derive enables derived events; its PublicBaseURL is supplied by the
	// manager from its settings.
	Derive DeriveOptions
	// Client and Mailer replace the default delivery transports (tests).
	Client Doer
	Mailer Mailer
	Logger *slog.Logger
}

// Endpoint problems reported to the console for an endpoint that is on disk
// but not running.
const (
	ProblemSecretMissing = "secret_missing"
	ProblemInvalid       = "invalid"
)

// EndpointView is the console's read of one endpoint. It never carries the
// secret or the full URL (a Discord or Slack URL is itself a credential, an
// ntfy topic is a capability): only scheme and host.
type EndpointView struct {
	ID                  string
	Name                string
	Format              Format
	URL                 string
	EventKinds          []string
	Disabled            bool
	AllowPrivateNetwork bool
	SecretSet           bool
	From                string
	To                  []string
	Username            string
	// Problem is empty for a running endpoint, else ProblemSecretMissing or
	// ProblemInvalid.
	Problem string
}

// EndpointInput is one console save. An empty ID creates an endpoint; an
// empty URL keeps the saved one; a nil Secret keeps the saved secret and an
// empty one clears it.
type EndpointInput struct {
	ID                  string
	Name                string
	Format              string
	URL                 string
	EventKinds          []string
	Disabled            bool
	AllowPrivateNetwork bool
	Secret              *string
	From                string
	To                  []string
	Username            string
}

// Settings are the console-managed options shared by every endpoint.
type Settings struct {
	PublicBaseURL string
}

// maxEndpoints bounds the configured endpoint count.
const maxEndpoints = 50

// maxNameRunes bounds an endpoint's display name.
const maxNameRunes = 80

// Manager owns the endpoint file, the secrets it names and the running
// emitter, so an owner can change webhooks from the console without a
// restart. Edits are serialized; each is validated in full before anything
// is written, and the running set only changes after the file is saved.
type Manager struct {
	opts    ManagerOptions
	logger  *slog.Logger
	state   *FileDeadLetter
	emitter *Emitter

	mu       sync.Mutex
	file     fileConfig
	problems map[string]string
	baseURL  string
}

// OpenManager loads the endpoint file and builds the emitter, which runs from
// boot and idles with no enabled endpoint. A malformed file is an error; an
// endpoint whose secret is missing or that fails validation is loaded but not
// run, and reported to the console as a problem.
func OpenManager(opts ManagerOptions) (*Manager, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if opts.Secrets == nil || opts.Events == nil || opts.ConfigPath == "" || opts.StatePath == "" {
		return nil, ErrConfigDiagnostic
	}
	state, err := OpenDeadLetter(opts.StatePath)
	if err != nil {
		logger.Error("webhook state read failed", "error", err)
		return nil, ErrDeadLetterDiagnostic
	}
	var fc fileConfig
	if _, err := jsonstate.Load(opts.ConfigPath, &fc); err != nil {
		logger.Error("webhook configuration decode failed", "error", err)
		return nil, ErrConfigDiagnostic
	}
	m := &Manager{opts: opts, logger: logger, state: state, file: fc, baseURL: fc.PublicBaseURL}
	running := m.resolveAll()
	cfg := Config{
		MaxAttempts:  fc.MaxAttempts,
		BaseBackoff:  time.Duration(fc.BaseBackoffSeconds) * time.Second,
		PollInterval: time.Duration(fc.PollIntervalMS) * time.Millisecond,
		Endpoints:    running,
	}
	em, err := buildEmitter(cfg, opts.Events, opts.Client, state, logger)
	if err != nil {
		return nil, err
	}
	if opts.Mailer != nil {
		em.mailer = opts.Mailer
	}
	derive := opts.Derive
	derive.PublicBaseURL = m.publicBaseURL
	em.EnableDerivation(derive)
	m.emitter = em
	return m, nil
}

// resolveAll resolves every endpoint on disk, records problems and returns the
// set that can run. Callers hold m.mu or own m exclusively.
func (m *Manager) resolveAll() []Endpoint {
	m.problems = map[string]string{}
	seen := map[string]bool{}
	var running []Endpoint
	for _, fe := range m.file.Endpoints {
		if seen[fe.ID] {
			m.problems[fe.ID] = ProblemInvalid
			continue
		}
		seen[fe.ID] = true
		ep, problem := m.resolve(fe)
		if problem != "" {
			m.problems[fe.ID] = problem
			m.logger.Warn("webhook endpoint not started", "endpoint", fe.ID, "problem", problem)
			continue
		}
		running = append(running, ep)
	}
	return running
}

// resolve builds one runtime endpoint, reading its secret.
func (m *Manager) resolve(fe fileEndpoint) (Endpoint, string) {
	var secret []byte
	if ref := strings.TrimSpace(fe.SecretRef); ref != "" {
		value, err := m.opts.Secrets.Secret(ref)
		if err != nil {
			return Endpoint{}, ProblemSecretMissing
		}
		secret = []byte(value)
	}
	ep := fe.endpoint(secret)
	if _, err := validateEndpoint(ep); err != nil {
		if len(secret) == 0 && needsSecret(ep) {
			return Endpoint{}, ProblemSecretMissing
		}
		return Endpoint{}, ProblemInvalid
	}
	return ep, ""
}

func needsSecret(ep Endpoint) bool {
	switch ep.format() {
	case FormatGeneric, FormatGotify:
		return true
	case FormatEmail:
		return ep.Username != ""
	}
	return false
}

// Run drives the emitter until ctx is canceled.
func (m *Manager) Run(ctx context.Context) { m.emitter.Run(ctx) }

func (m *Manager) publicBaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseURL
}

// Endpoints lists every endpoint on disk, in file order.
func (m *Manager) Endpoints() []EndpointView {
	m.mu.Lock()
	defer m.mu.Unlock()
	views := make([]EndpointView, 0, len(m.file.Endpoints))
	for _, fe := range m.file.Endpoints {
		views = append(views, m.view(fe))
	}
	return views
}

func (m *Manager) view(fe fileEndpoint) EndpointView {
	format, ok := ParseFormat(fe.Format)
	if !ok {
		format = Format(fe.Format)
	}
	secretSet := false
	if ref := strings.TrimSpace(fe.SecretRef); ref != "" {
		if value, err := m.opts.Secrets.Secret(ref); err == nil && value != "" {
			secretSet = true
		}
	}
	return EndpointView{
		ID:                  fe.ID,
		Name:                fe.Name,
		Format:              format,
		URL:                 redactURL(fe.URL),
		EventKinds:          append([]string(nil), fe.EventKinds...),
		Disabled:            fe.Disabled,
		AllowPrivateNetwork: fe.AllowPrivateNetwork,
		SecretSet:           secretSet,
		From:                fe.From,
		To:                  append([]string(nil), fe.To...),
		Username:            fe.Username,
		Problem:             m.problems[fe.ID],
	}
}

// redactURL keeps only scheme, host and port, marking a dropped path or query
// with an ellipsis.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		out += "/…"
	}
	return out
}

// SaveEndpoint creates or replaces one endpoint. Nothing is written and the
// running set is unchanged unless the whole new set validates.
func (m *Manager) SaveEndpoint(in EndpointInput) (EndpointView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	index := -1
	var prior fileEndpoint
	if in.ID != "" {
		for i, fe := range m.file.Endpoints {
			if fe.ID == in.ID {
				index, prior = i, fe
				break
			}
		}
		if index < 0 {
			return EndpointView{}, ErrUnknownEndpoint
		}
	} else if len(m.file.Endpoints) >= maxEndpoints {
		return EndpointView{}, ErrConfigDiagnostic
	}

	fe, err := m.buildFileEndpoint(in, prior, index >= 0)
	if err != nil {
		return EndpointView{}, err
	}

	// The secret the endpoint runs with: a new value, or the saved one.
	secret := ""
	if in.Secret != nil {
		secret = *in.Secret
	} else if prior.SecretRef != "" {
		if value, err := m.opts.Secrets.Secret(prior.SecretRef); err == nil {
			secret = value
		}
	}
	if secret == "" {
		fe.SecretRef = ""
	} else if fe.SecretRef == "" {
		fe.SecretRef = "webhook-" + fe.ID
	}
	ep := fe.endpoint([]byte(secret))
	if _, err := validateEndpoint(ep); err != nil {
		return EndpointView{}, ErrConfigDiagnostic
	}

	next := m.file
	next.Endpoints = append([]fileEndpoint(nil), m.file.Endpoints...)
	if index >= 0 {
		next.Endpoints[index] = fe
	} else {
		next.Endpoints = append(next.Endpoints, fe)
	}
	running, err := m.runningSetWith(next, fe.ID, ep)
	if err != nil {
		return EndpointView{}, err
	}
	if err := validateSet(running); err != nil {
		return EndpointView{}, ErrConfigDiagnostic
	}

	restore, err := m.writeSecret(in.Secret != nil || prior.SecretRef != fe.SecretRef, fe.SecretRef, secret)
	if err != nil {
		return EndpointView{}, err
	}
	if err := jsonstate.Save(m.opts.ConfigPath, next); err != nil {
		m.logger.Error("webhook configuration write failed", "error", err)
		restore()
		return EndpointView{}, ErrConfigDiagnostic
	}
	if prior.SecretRef != "" && prior.SecretRef != fe.SecretRef && !refInUse(next, prior.SecretRef) {
		if err := m.opts.Secrets.SetSecret(prior.SecretRef, ""); err != nil {
			m.logger.Warn("webhook secret removal failed", "endpoint", fe.ID, "error", ErrSecretDiagnostic)
		}
	}
	if err := m.emitter.SetEndpoints(running); err != nil {
		return EndpointView{}, ErrConfigDiagnostic
	}
	m.file = next
	delete(m.problems, fe.ID)
	return m.view(fe), nil
}

// buildFileEndpoint applies a console input over the saved endpoint.
func (m *Manager) buildFileEndpoint(in EndpointInput, prior fileEndpoint, existing bool) (fileEndpoint, error) {
	format, ok := ParseFormat(in.Format)
	if !ok {
		return fileEndpoint{}, ErrConfigDiagnostic
	}
	name := strings.TrimSpace(stripControls(in.Name))
	if utf8.RuneCountInString(name) > maxNameRunes {
		return fileEndpoint{}, ErrConfigDiagnostic
	}
	fe := fileEndpoint{
		ID:                  prior.ID,
		Name:                name,
		Format:              string(format),
		URL:                 strings.TrimSpace(in.URL),
		SecretRef:           prior.SecretRef,
		EventKinds:          cleanKinds(in.EventKinds),
		Disabled:            in.Disabled,
		AllowPrivateNetwork: in.AllowPrivateNetwork,
	}
	if !existing {
		id, err := newEndpointID()
		if err != nil {
			return fileEndpoint{}, ErrConfigDiagnostic
		}
		fe.ID = id
	}
	if fe.URL == "" {
		if !existing || prior.Format != string(format) && (prior.Format != "" || format != FormatGeneric) {
			return fileEndpoint{}, ErrConfigDiagnostic // a new endpoint, or a format change, needs a URL
		}
		fe.URL = prior.URL
	}
	if format == FormatEmail {
		fe.From = strings.TrimSpace(in.From)
		fe.Username = strings.TrimSpace(in.Username)
		for _, to := range in.To {
			if to = strings.TrimSpace(to); to != "" {
				fe.To = append(fe.To, to)
			}
		}
	}
	return fe, nil
}

func cleanKinds(kinds []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

func newEndpointID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "wh-" + hex.EncodeToString(b[:]), nil
}

// runningSetWith returns the endpoints that would run under next, with the
// edited endpoint resolved from its in-memory secret and every other
// endpoint taken from the running emitter (or left out while it has a
// problem).
func (m *Manager) runningSetWith(next fileConfig, editedID string, edited Endpoint) ([]Endpoint, error) {
	current := map[string]Endpoint{}
	for _, ep := range m.emitter.endpoints() {
		current[ep.ID] = ep
	}
	running := make([]Endpoint, 0, len(next.Endpoints))
	for _, fe := range next.Endpoints {
		if fe.ID == editedID {
			running = append(running, edited)
			continue
		}
		if ep, ok := current[fe.ID]; ok {
			running = append(running, ep)
		}
	}
	return running, nil
}

// writeSecret stores the edited endpoint's secret file when it changed and
// returns a function that puts the previous value back, for a config save
// that fails after the secret is written.
func (m *Manager) writeSecret(changed bool, ref, value string) (func(), error) {
	noop := func() {}
	if !changed || ref == "" {
		return noop, nil
	}
	previous, err := m.opts.Secrets.Secret(ref)
	if err != nil {
		previous = ""
	}
	if err := m.opts.Secrets.SetSecret(ref, value); err != nil {
		m.logger.Error("webhook secret write failed", "error", ErrSecretDiagnostic)
		return noop, ErrSecretDiagnostic
	}
	return func() {
		if err := m.opts.Secrets.SetSecret(ref, previous); err != nil {
			m.logger.Error("webhook secret restore failed", "error", ErrSecretDiagnostic)
		}
	}, nil
}

func refInUse(fc fileConfig, ref string) bool {
	for _, fe := range fc.Endpoints {
		if fe.SecretRef == ref {
			return true
		}
	}
	return false
}

// DeleteEndpoint removes one endpoint and, unless another endpoint names the
// same ref, its secret file.
func (m *Manager) DeleteEndpoint(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := -1
	for i, fe := range m.file.Endpoints {
		if fe.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrUnknownEndpoint
	}
	removed := m.file.Endpoints[index]
	next := m.file
	next.Endpoints = append(append([]fileEndpoint(nil), m.file.Endpoints[:index]...), m.file.Endpoints[index+1:]...)
	var running []Endpoint
	for _, ep := range m.emitter.endpoints() {
		if ep.ID != id {
			running = append(running, ep)
		}
	}
	if err := jsonstate.Save(m.opts.ConfigPath, next); err != nil {
		m.logger.Error("webhook configuration write failed", "error", err)
		return ErrConfigDiagnostic
	}
	if err := m.emitter.SetEndpoints(running); err != nil {
		return ErrConfigDiagnostic
	}
	m.file = next
	delete(m.problems, id)
	if removed.SecretRef != "" && !refInUse(next, removed.SecretRef) {
		if err := m.opts.Secrets.SetSecret(removed.SecretRef, ""); err != nil {
			m.logger.Warn("webhook secret removal failed", "endpoint", id, "error", ErrSecretDiagnostic)
		}
	}
	return nil
}

// TestEndpoint sends one test notification to a running endpoint.
func (m *Manager) TestEndpoint(ctx context.Context, id string) (TestResult, error) {
	m.mu.Lock()
	problem, hasProblem := m.problems[id]
	m.mu.Unlock()
	if hasProblem {
		return TestResult{OK: false, Diagnostic: problemDiagnostic(problem)}, nil
	}
	return m.emitter.Test(ctx, id)
}

func problemDiagnostic(problem string) string {
	if problem == ProblemSecretMissing {
		return ErrSecretDiagnostic.Error()
	}
	return ErrConfigDiagnostic.Error()
}

// Deliveries returns the delivery log, newest first.
func (m *Manager) Deliveries() []Delivery { return m.state.Deliveries() }

// Settings returns the shared options.
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Settings{PublicBaseURL: m.baseURL}
}

// SetSettings validates and saves the shared options.
func (m *Manager) SetSettings(s Settings) (Settings, error) {
	base := strings.TrimRight(strings.TrimSpace(s.PublicBaseURL), "/")
	if base != "" && !validBaseURL(base) {
		return Settings{}, ErrConfigDiagnostic
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.file
	next.PublicBaseURL = base
	if err := jsonstate.Save(m.opts.ConfigPath, next); err != nil {
		m.logger.Error("webhook configuration write failed", "error", err)
		return Settings{}, ErrConfigDiagnostic
	}
	m.file = next
	m.baseURL = base
	return Settings{PublicBaseURL: base}, nil
}

// validBaseURL accepts an absolute http(s) origin with an optional path and no
// user info, query or fragment.
func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http"
}
