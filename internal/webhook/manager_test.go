package webhook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/jsonstate"
)

// memSecrets is an in-memory SecretStore.
type memSecrets struct {
	mu     sync.Mutex
	values map[string]string
	fail   bool
}

func (m *memSecrets) Secret(ref string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[ref]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}

func (m *memSecrets) SetSecret(ref, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("write failed")
	}
	if value == "" {
		delete(m.values, ref)
		return nil
	}
	m.values[ref] = value
	return nil
}

type managerFixture struct {
	m       *Manager
	secrets *memSecrets
	doer    *recordingDoer
	config  string
}

func newManagerFixture(t *testing.T, initial string) *managerFixture {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "webhook-endpoints.json")
	if initial != "" {
		if err := os.WriteFile(config, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := &managerFixture{secrets: &memSecrets{values: map[string]string{}}, doer: &recordingDoer{}, config: config}
	m, err := OpenManager(ManagerOptions{
		ConfigPath: config,
		StatePath:  filepath.Join(dir, "webhook-dead-letter.json"),
		Secrets:    f.secrets,
		Events:     &fixedEvents{},
		Client:     f.doer,
	})
	if err != nil {
		t.Fatalf("OpenManager: %v", err)
	}
	f.m = m
	return f
}

func strPtr(s string) *string { return &s }

func (f *managerFixture) file(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestManagerAddsDiscordEndpointWithRedactedURL(t *testing.T) {
	f := newManagerFixture(t, "")
	view, err := f.m.SaveEndpoint(EndpointInput{
		Name:       "Family chat",
		Format:     "discord",
		URL:        "https://discord.example/api/webhooks/123/tokenvalue",
		EventKinds: []string{KindMediaAdded},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.HasPrefix(view.ID, "wh-") || view.Format != FormatDiscord || view.Problem != "" {
		t.Fatalf("view = %+v", view)
	}
	if view.URL != "https://discord.example/…" {
		t.Fatalf("URL must be redacted to its origin, got %q", view.URL)
	}
	if len(f.m.emitter.endpoints()) != 1 {
		t.Fatal("a saved endpoint must run without a restart")
	}
	if !strings.Contains(f.file(t), "tokenvalue") {
		t.Fatal("the full URL is kept in the endpoint file")
	}
	if list := f.m.Endpoints(); len(list) != 1 || strings.Contains(list[0].URL, "tokenvalue") {
		t.Fatalf("list = %+v", list)
	}
}

func TestManagerStoresSecretOutsideConfig(t *testing.T) {
	f := newManagerFixture(t, "")
	view, err := f.m.SaveEndpoint(EndpointInput{Format: "generic", URL: "https://hooks.example/in", Secret: strPtr("signing-key")})
	if err != nil {
		t.Fatal(err)
	}
	if !view.SecretSet {
		t.Fatal("secretSet must report the stored secret")
	}
	if strings.Contains(f.file(t), "signing-key") {
		t.Fatal("the secret must never be written to the endpoint file")
	}
	ref := "webhook-" + view.ID
	if got, _ := f.secrets.Secret(ref); got != "signing-key" {
		t.Fatalf("secret file %s = %q", ref, got)
	}

	// An edit with no URL and no secret keeps both.
	view, err = f.m.SaveEndpoint(EndpointInput{ID: view.ID, Format: "generic", Name: "Renamed"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if view.Name != "Renamed" || !view.SecretSet || view.URL != "https://hooks.example/…" {
		t.Fatalf("edit view = %+v", view)
	}
	running := f.m.emitter.endpoints()
	if len(running) != 1 || string(running[0].Secret) != "signing-key" {
		t.Fatal("the running endpoint must keep its secret across an edit")
	}

	if err := f.m.DeleteEndpoint(view.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.secrets.Secret(ref); err == nil {
		t.Fatal("delete must remove the secret file")
	}
	if len(f.m.emitter.endpoints()) != 0 || len(f.m.Endpoints()) != 0 {
		t.Fatal("delete must stop and remove the endpoint")
	}
	if err := f.m.DeleteEndpoint(view.ID); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestManagerInvalidSaveChangesNothing(t *testing.T) {
	f := newManagerFixture(t, "")
	good, err := f.m.SaveEndpoint(EndpointInput{Format: "ntfy", URL: "https://ntfy.example/topic"})
	if err != nil {
		t.Fatal(err)
	}
	before := f.file(t)
	cases := []EndpointInput{
		{Format: "discord", URL: "https://discord.example/x", EventKinds: []string{"media.created"}},
		{Format: "generic", URL: "https://hooks.example/in"}, // generic needs a signing secret
		{Format: "gotify", URL: "https://gotify.example"},    // gotify needs a token
		{Format: "slack", URL: "http://169.254.169.254/x"},
		{Format: "email", URL: "smtps://mail.example", From: "a@example.com"}, // no recipients
		{Format: "carrier-pigeon", URL: "https://x.example"},
		{Format: "ntfy", URL: "https://user:pw@ntfy.example/t"}, // #nosec G101 -- a refused user-info test input, not a credential.
		{Format: "ntfy", URL: "https://ntfy.example/t", Name: strings.Repeat("n", maxNameRunes+1)},
		{ID: good.ID, Format: "slack"}, // a format change needs a URL
	}
	for i, in := range cases {
		if _, err := f.m.SaveEndpoint(in); err == nil {
			t.Errorf("case %d: invalid input accepted", i)
		}
	}
	if f.file(t) != before {
		t.Fatal("a rejected save must not change the endpoint file")
	}
	if len(f.secrets.values) != 0 {
		t.Fatal("a rejected save must not write a secret")
	}
	if running := f.m.emitter.endpoints(); len(running) != 1 || running[0].ID != good.ID {
		t.Fatalf("running set changed: %+v", running)
	}
}

func TestManagerSecretWriteFailureKeepsConfig(t *testing.T) {
	f := newManagerFixture(t, "")
	f.secrets.fail = true
	if _, err := f.m.SaveEndpoint(EndpointInput{Format: "gotify", URL: "https://gotify.example", Secret: strPtr("tok")}); !errors.Is(err, ErrSecretDiagnostic) {
		t.Fatalf("want the secret diagnostic, got %v", err)
	}
	if _, err := os.Stat(f.config); !os.IsNotExist(err) {
		t.Fatal("no endpoint file may be written when the secret write fails")
	}
}

func TestManagerReportsEndpointsThatCannotRun(t *testing.T) {
	f := newManagerFixture(t, `{"endpoints":[
		{"id":"a","url":"https://hooks.example/a","secret_ref":"missing-ref"},
		{"id":"b","format":"discord","url":"https://discord.example/x","event_kinds":["media.created"]},
		{"id":"c","format":"ntfy","url":"https://ntfy.example/t"}
	]}`)
	views := f.m.Endpoints()
	if len(views) != 3 {
		t.Fatalf("every endpoint on disk is listed, got %d", len(views))
	}
	if views[0].Problem != ProblemSecretMissing || views[1].Problem != ProblemInvalid || views[2].Problem != "" {
		t.Fatalf("problems = %q %q %q", views[0].Problem, views[1].Problem, views[2].Problem)
	}
	if running := f.m.emitter.endpoints(); len(running) != 1 || running[0].ID != "c" {
		t.Fatalf("only the valid endpoint runs: %+v", running)
	}
	res, err := f.m.TestEndpoint(context.Background(), "a")
	if err != nil || res.OK || res.Diagnostic != ErrSecretDiagnostic.Error() {
		t.Fatalf("test of a broken endpoint = %+v, %v", res, err)
	}

	// Supplying the secret repairs the endpoint without a restart.
	view, err := f.m.SaveEndpoint(EndpointInput{ID: "a", Format: "generic", Secret: strPtr("key")})
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if view.Problem != "" || len(f.m.emitter.endpoints()) != 2 {
		t.Fatalf("repair view = %+v", view)
	}
}

func TestManagerTestSendAndDeliveryLog(t *testing.T) {
	f := newManagerFixture(t, "")
	view, err := f.m.SaveEndpoint(EndpointInput{Format: "slack", URL: "https://hooks.slack.example/services/x", Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.m.TestEndpoint(context.Background(), view.ID)
	if err != nil || !res.OK {
		t.Fatalf("test = %+v, %v", res, err)
	}
	log := f.m.Deliveries()
	if len(log) != 1 || log[0].EndpointID != view.ID || !log[0].Test {
		t.Fatalf("delivery log = %+v", log)
	}
	if _, err := f.m.TestEndpoint(context.Background(), "wh-none"); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("unknown endpoint: %v", err)
	}
}

func TestManagerSettingsValidateBaseURL(t *testing.T) {
	f := newManagerFixture(t, "")
	for _, bad := range []string{"javascript:alert(1)", "ftp://media.example", "https://u:p@media.example", "media.example", "https://media.example/?q=1"} {
		if _, err := f.m.SetSettings(Settings{PublicBaseURL: bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	got, err := f.m.SetSettings(Settings{PublicBaseURL: "https://media.example/"})
	if err != nil || got.PublicBaseURL != "https://media.example" {
		t.Fatalf("settings = %+v, %v", got, err)
	}
	if f.m.publicBaseURL() != "https://media.example" || !strings.Contains(f.file(t), "public_base_url") {
		t.Fatal("settings must apply and persist")
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{ // #nosec G101 -- redaction test inputs, not credentials.
		"https://hooks.example":           "https://hooks.example",
		"https://hooks.example/":          "https://hooks.example",
		"https://ntfy.example:8443/topic": "https://ntfy.example:8443/…",
		"https://hooks.example/?token=x":  "https://hooks.example/…",
		"smtps://mail.example:465":        "smtps://mail.example:465",
		"not a url":                       "",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManagerHandlesPreAndPostRenameDirectorySyncFailures(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "webhook-endpoints.json")
	statePath := filepath.Join(dir, "webhook-dead-letter.json")
	secrets := &memSecrets{values: map[string]string{}}

	m, err := OpenManager(ManagerOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		Secrets:    secrets,
		Events:     &fixedEvents{},
		Client:     &recordingDoer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldVal := "val-a"
	created, err := m.SaveEndpoint(EndpointInput{
		Format: "slack",
		URL:    "https://hooks.slack.example/services/old",
		Secret: &oldVal,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldRef := m.file.Endpoints[0].SecretRef

	// Post-rename syncDir failure: new config IS on disk, so runtime and secret
	// must stay on the new values instead of restoring the old signing secret.
	injected := errors.New("dir fsync EIO")
	restoreSync := jsonstate.WithSyncDirForTest(func(string) error { return injected })
	newVal := "val-b"
	if _, err := m.SaveEndpoint(EndpointInput{
		ID:     created.ID,
		Format: "slack",
		URL:    "https://hooks.slack.example/services/new",
		Secret: &newVal,
	}); err == nil {
		restoreSync()
		t.Fatal("expected error on post-rename syncDir failure")
	}
	restoreSync()

	// Runtime and secret store must both reflect the installed new endpoint and secret.
	if got := m.emitter.endpoints(); len(got) != 1 || got[0].URL != "https://hooks.slack.example/services/new" {
		t.Fatalf("runtime emitter after post-rename sync error = %+v", got)
	}
	if gotSec, err := secrets.Secret(oldRef); err != nil || gotSec != newVal {
		t.Fatalf("secret after post-rename sync error = (%q, %v), want %q (not restored to old)", gotSec, err, newVal)
	}

	// Retry cleanly once syncDir recovers.
	retried, err := m.SaveEndpoint(EndpointInput{
		ID:     created.ID,
		Format: "slack",
		URL:    "https://hooks.slack.example/services/new",
		Secret: &newVal,
	})
	if err != nil || retried.URL != "https://hooks.slack.example/…" {
		t.Fatalf("retry SaveEndpoint = (%+v, %v)", retried, err)
	}

	// Restart from disk agrees with runtime & secrets.
	restarted, err := OpenManager(ManagerOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		Secrets:    secrets,
		Events:     &fixedEvents{},
		Client:     &recordingDoer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eps := restarted.Endpoints(); len(eps) != 1 || eps[0].URL != "https://hooks.slack.example/…" {
		t.Fatalf("restarted endpoints = %+v", eps)
	}
	if len(restarted.file.Endpoints) != 1 || restarted.file.Endpoints[0].URL != "https://hooks.slack.example/services/new" {
		t.Fatalf("restarted file endpoints = %+v", restarted.file.Endpoints)
	}
	if gotSec, err := secrets.Secret(restarted.file.Endpoints[0].SecretRef); err != nil || gotSec != newVal {
		t.Fatalf("restarted secret = (%q, %v), want %q", gotSec, err, newVal)
	}

	// Post-rename syncDir failure on DeleteEndpoint removes runtime endpoint,
	// defers secret deletion until durable save, and still preserves runtime/disk agreement.
	delRef := restarted.file.Endpoints[0].SecretRef
	restoreDelSync := jsonstate.WithSyncDirForTest(func(string) error { return injected })
	if err := restarted.DeleteEndpoint(created.ID); err == nil {
		restoreDelSync()
		t.Fatal("expected error on DeleteEndpoint post-rename sync failure")
	}
	restoreDelSync()
	if got := restarted.emitter.endpoints(); len(got) != 0 {
		t.Fatalf("runtime emitter after post-rename DeleteEndpoint sync error = %+v, want empty", got)
	}
	if gotSec, err := secrets.Secret(delRef); err != nil || gotSec != newVal {
		t.Fatalf("deleted endpoint secret %q = (%q, %v), want preserved until durable sync", delRef, gotSec, err)
	}
	// A subsequent durable SetSettings purges the deferred orphaned secret.
	if _, err := restarted.SetSettings(Settings{}); err != nil {
		t.Fatalf("SetSettings cleanup: %v", err)
	}
	if _, err := secrets.Secret(delRef); err == nil {
		t.Fatalf("deleted endpoint secret %q still present after subsequent durable save", delRef)
	}

	// Post-rename syncDir failure on create returns the new ID and retrying the
	// create input without an ID reuses that endpoint instead of duplicating.
	createVal := "val-c"
	restoreCreateSync := jsonstate.WithSyncDirForTest(func(string) error { return injected })
	failedView, createErr := restarted.SaveEndpoint(EndpointInput{
		Format: "slack",
		URL:    "https://hooks.slack.example/services/create",
		Secret: &createVal,
	})
	restoreCreateSync()
	if createErr == nil || failedView.ID == "" {
		t.Fatalf("post-rename failed create = (%+v, %v), want non-empty ID and error", failedView, createErr)
	}
	retriedCreate, err := restarted.SaveEndpoint(EndpointInput{
		Format: "slack",
		URL:    "https://hooks.slack.example/services/create",
		Secret: &createVal,
	})
	if err != nil || retriedCreate.ID != failedView.ID {
		t.Fatalf("create retry = (%+v, %v), want reused ID %q", retriedCreate, err, failedView.ID)
	}
	if eps := restarted.Endpoints(); len(eps) != 1 || len(restarted.emitter.endpoints()) != 1 {
		t.Fatalf("duplicate endpoints created on create retry: views=%+v emitter=%+v", eps, restarted.emitter.endpoints())
	}
	// Secret clear update under post-rename syncDir error defers secret deletion
	// until a later durable save confirms the updated config on disk.
	clearRef := restarted.file.Endpoints[0].SecretRef
	emptySec := ""
	restoreClearSync := jsonstate.WithSyncDirForTest(func(string) error { return injected })
	if _, err := restarted.SaveEndpoint(EndpointInput{
		ID:     retriedCreate.ID,
		Format: "slack",
		URL:    "https://hooks.slack.example/services/create",
		Secret: &emptySec,
	}); err == nil {
		restoreClearSync()
		t.Fatal("expected error on secret-clear post-rename syncDir failure")
	}
	restoreClearSync()
	if gotSec, err := secrets.Secret(clearRef); err != nil || gotSec != createVal {
		t.Fatalf("cleared secret %q = (%q, %v), want preserved until durable sync", clearRef, gotSec, err)
	}
	if _, err := restarted.SetSettings(Settings{}); err != nil {
		t.Fatalf("SetSettings cleanup after clear: %v", err)
	}
	if _, err := secrets.Secret(clearRef); err == nil {
		t.Fatalf("cleared secret %q still present after durable save", clearRef)
	}
	if err := restarted.DeleteEndpoint(retriedCreate.ID); err != nil {
		t.Fatalf("cleanup DeleteEndpoint: %v", err)
	}

	// Pre-rename failure on another manager (config path is an existing directory)
	// MUST restore old secret and keep old runtime state.
	preDir := t.TempDir()
	preConfig := filepath.Join(preDir, "webhook-endpoints.json")
	preSecrets := &memSecrets{values: map[string]string{}}
	preM, err := OpenManager(ManagerOptions{
		ConfigPath: preConfig,
		StatePath:  filepath.Join(preDir, "webhook-dead-letter.json"),
		Secrets:    preSecrets,
		Events:     &fixedEvents{},
		Client:     &recordingDoer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	v0, err := preM.SaveEndpoint(EndpointInput{
		Format: "slack",
		URL:    "https://hooks.slack.example/services/v0",
		Secret: &oldVal,
	})
	if err != nil {
		t.Fatal(err)
	}
	preRef := preM.file.Endpoints[0].SecretRef
	// Force pre-rename failure by replacing ConfigPath with a directory.
	if err := os.Remove(preConfig); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(preConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := preM.SaveEndpoint(EndpointInput{
		ID:     v0.ID,
		Format: "slack",
		URL:    "https://hooks.slack.example/services/v1",
		Secret: &newVal,
	}); err == nil {
		t.Fatal("expected pre-rename failure when ConfigPath is a directory")
	}
	if gotSec, err := preSecrets.Secret(preRef); err != nil || gotSec != oldVal {
		t.Fatalf("secret after pre-rename failure = (%q, %v), want %q restored", gotSec, err, oldVal)
	}
	if eps := preM.Endpoints(); len(eps) != 1 || eps[0].URL != "https://hooks.slack.example/…" {
		t.Fatalf("runtime endpoints after pre-rename failure = %+v", eps)
	}
	if got := preM.emitter.endpoints(); len(got) != 1 || got[0].URL != "https://hooks.slack.example/services/v0" {
		t.Fatalf("runtime emitter after pre-rename failure = %+v", got)
	}
}
