package graph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jsonstate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/webhook"
)

// fakeWebhooks records what the console fields asked the manager to do.
type fakeWebhooks struct {
	saved    []webhook.EndpointInput
	deleted  []string
	tested   []string
	settings webhook.Settings
}

func (f *fakeWebhooks) Endpoints() []webhook.EndpointView {
	return []webhook.EndpointView{{ID: "hook-1", Name: "Alerts", Format: webhook.FormatDiscord, URL: "https://chat.example", SecretSet: true}}
}

func (f *fakeWebhooks) SaveEndpoint(in webhook.EndpointInput) (webhook.EndpointView, error) {
	if in.Format == "bogus" {
		return webhook.EndpointView{}, webhook.ErrConfigDiagnostic
	}
	f.saved = append(f.saved, in)
	return webhook.EndpointView{ID: "hook-2", Name: in.Name, Format: webhook.Format(in.Format), URL: "https://chat.example"}, nil
}

func (f *fakeWebhooks) DeleteEndpoint(id string) error {
	if id != "hook-1" {
		return webhook.ErrUnknownEndpoint
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeWebhooks) TestEndpoint(_ context.Context, id string) (webhook.TestResult, error) {
	f.tested = append(f.tested, id)
	return webhook.TestResult{OK: false, StatusClass: "4xx", Diagnostic: "webhook endpoint rejected delivery", DurationMs: 12}, nil
}

func (f *fakeWebhooks) Deliveries() []webhook.Delivery {
	return []webhook.Delivery{{EndpointID: "hook-1", EventKind: webhook.KindTest, Outcome: "failed", StatusClass: "4xx", Attempts: 1, At: time.Unix(0, 0), Test: true}}
}

func (f *fakeWebhooks) Settings() webhook.Settings { return f.settings }

func (f *fakeWebhooks) SetSettings(s webhook.Settings) (webhook.Settings, error) {
	f.settings = s
	return s, nil
}

// The webhook fields are Admin-Session gated, reach the manager with the
// console's input, and log each configuration change.
func TestWebhookFieldsAreDispatchedAndAdminGated(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	fake := &fakeWebhooks{}
	handler.SetWebhooks(fake)

	const (
		list     = `{"query":"query Webhooks { webhooks { id name format url secretSet eventKinds to problem } }"}`
		save     = `{"query":"mutation SaveWebhook($name: String, $format: String, $url: String, $secret: String, $eventKinds: [String!]) { saveWebhook(name: $name, format: $format, url: $url, secret: $secret, eventKinds: $eventKinds) { id name format } }","variables":{"name":"Chat","format":"discord","url":"https://chat.example/hook/token","secret":"s3cret","eventKinds":["media.added"]}}`
		badSave  = `{"query":"mutation SaveWebhook($format: String) { saveWebhook(format: $format) { id } }","variables":{"format":"bogus"}}`
		del      = `{"query":"mutation DeleteWebhook($id: String!) { deleteWebhook(id: $id) }","variables":{"id":"hook-1"}}`
		delOther = `{"query":"mutation DeleteWebhook($id: String!) { deleteWebhook(id: $id) }","variables":{"id":"nope"}}`
		test     = `{"query":"mutation TestWebhook($id: String!) { testWebhook(id: $id) { ok statusClass diagnostic durationMs } }","variables":{"id":"hook-1"}}`
		logs     = `{"query":"query WebhookDeliveries { webhookDeliveries { endpointID eventKind outcome attempts at test } }"}`
		settings = `{"query":"query WebhookSettings { webhookSettings { publicBaseURL formats eventKinds } }"}`
		setBase  = `{"query":"mutation SetWebhookSettings($publicBaseURL: String) { setWebhookSettings(publicBaseURL: $publicBaseURL) { publicBaseURL } }","variables":{"publicBaseURL":"https://media.example"}}`
	)

	for name, body := range map[string]string{"webhooks": list, "saveWebhook": save, "deleteWebhook": del, "testWebhook": test, "webhookDeliveries": logs, "webhookSettings": settings, "setWebhookSettings": setBase} {
		if _, errs := postLibraryTrash(t, handler, body); len(errs) == 0 || errs[0] != "admin session required" {
			t.Fatalf("%s without an Admin Session: errors = %q, want admin session required", name, errs)
		}
	}

	openManagerAdminSession(t, st)
	data, errs := postLibraryTrash(t, handler, list)
	var listed []WebhookEndpointInfo
	if len(errs) != 0 || json.Unmarshal(data["webhooks"], &listed) != nil || len(listed) != 1 || !listed[0].SecretSet || listed[0].Format != "discord" {
		t.Fatalf("webhooks = %s, errors %q", data["webhooks"], errs)
	}

	data, errs = postLibraryTrash(t, handler, save)
	if len(errs) != 0 || !strings.Contains(string(data["saveWebhook"]), `"id":"hook-2"`) {
		t.Fatalf("saveWebhook = %s, errors %q", data["saveWebhook"], errs)
	}
	if len(fake.saved) != 1 || fake.saved[0].Secret == nil || *fake.saved[0].Secret != "s3cret" || fake.saved[0].URL != "https://chat.example/hook/token" || len(fake.saved[0].EventKinds) != 1 {
		t.Fatalf("saved input = %+v", fake.saved)
	}
	if _, errs := postLibraryTrash(t, handler, badSave); len(errs) == 0 || errs[0] != "webhook configuration is invalid" {
		t.Fatalf("invalid saveWebhook errors = %q, want the fixed diagnostic", errs)
	}

	data, errs = postLibraryTrash(t, handler, test)
	var tested WebhookTestResultInfo
	if len(errs) != 0 || json.Unmarshal(data["testWebhook"], &tested) != nil || tested.OK || tested.StatusClass != "4xx" || tested.DurationMs != 12 {
		t.Fatalf("testWebhook = %s, errors %q", data["testWebhook"], errs)
	}

	data, errs = postLibraryTrash(t, handler, logs)
	if len(errs) != 0 || !strings.Contains(string(data["webhookDeliveries"]), `"at":"1970-01-01T00:00:00Z"`) {
		t.Fatalf("webhookDeliveries = %s, errors %q", data["webhookDeliveries"], errs)
	}

	data, errs = postLibraryTrash(t, handler, setBase)
	if len(errs) != 0 || fake.settings.PublicBaseURL != "https://media.example" {
		t.Fatalf("setWebhookSettings = %s, errors %q", data["setWebhookSettings"], errs)
	}
	data, errs = postLibraryTrash(t, handler, settings)
	var got WebhookSettingsInfo
	if len(errs) != 0 || json.Unmarshal(data["webhookSettings"], &got) != nil || got.PublicBaseURL != "https://media.example" || len(got.Formats) != 6 || len(got.EventKinds) != len(webhook.DerivedKinds) {
		t.Fatalf("webhookSettings = %s, errors %q", data["webhookSettings"], errs)
	}

	if _, errs := postLibraryTrash(t, handler, delOther); len(errs) == 0 || errs[0] != "webhook endpoint not found" {
		t.Fatalf("deleteWebhook unknown errors = %q", errs)
	}
	data, errs = postLibraryTrash(t, handler, del)
	if len(errs) != 0 || string(data["deleteWebhook"]) != "true" || len(fake.deleted) != 1 {
		t.Fatalf("deleteWebhook = %s, errors %q", data["deleteWebhook"], errs)
	}

	entries, err := st.ActivityEntriesAfter(context.Background(), 0, []string{store.ActivityConfigUpdate}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("config.update entries = %d, want save, settings and delete", len(entries))
	}
	for _, entry := range entries {
		if entry.TargetType != "webhook" {
			t.Fatalf("activity target = %q, want webhook", entry.TargetType)
		}
	}
}

type postRenameSyncFailWebhooks struct {
	fakeWebhooks
	fail bool
}

func (f *postRenameSyncFailWebhooks) SaveEndpoint(in webhook.EndpointInput) (webhook.EndpointView, error) {
	f.saved = append(f.saved, in)
	view := webhook.EndpointView{ID: "hook-installed", Name: in.Name, Format: webhook.Format(in.Format), URL: "https://chat.example"}
	if f.fail {
		return view, errors.Join(webhook.ErrConfigDiagnostic, &jsonstate.PostRenameSyncError{Err: errors.New("dir sync EIO")})
	}
	return view, nil
}

func (f *postRenameSyncFailWebhooks) DeleteEndpoint(id string) error {
	f.deleted = append(f.deleted, id)
	if f.fail {
		return errors.Join(webhook.ErrConfigDiagnostic, &jsonstate.PostRenameSyncError{Err: errors.New("dir sync EIO")})
	}
	return nil
}

func (f *postRenameSyncFailWebhooks) SetSettings(s webhook.Settings) (webhook.Settings, error) {
	f.settings = s
	if f.fail {
		return s, errors.Join(webhook.ErrConfigDiagnostic, &jsonstate.PostRenameSyncError{Err: errors.New("dir sync EIO")})
	}
	return s, nil
}

func TestWebhookMutationsRecordActivityOnPostRenameSyncFailure(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	stub := &postRenameSyncFailWebhooks{fail: true}
	handler.SetWebhooks(stub)
	openManagerAdminSession(t, st)

	saveQuery := `{"query":"mutation SaveWebhook($format: String, $url: String) { saveWebhook(format: $format, url: $url) { id } }","variables":{"format":"discord","url":"https://chat.example/hook/token"}}`
	if _, errs := postLibraryTrash(t, handler, saveQuery); len(errs) == 0 {
		t.Fatal("expected error from saveWebhook on post-rename sync failure")
	}
	settingsQuery := `{"query":"mutation SetWebhookSettings($publicBaseURL: String) { setWebhookSettings(publicBaseURL: $publicBaseURL) { publicBaseURL } }","variables":{"publicBaseURL":"https://media.example"}}`
	if _, errs := postLibraryTrash(t, handler, settingsQuery); len(errs) == 0 {
		t.Fatal("expected error from setWebhookSettings on post-rename sync failure")
	}
	if _, errs := postLibraryTrash(t, handler, `{"query":"mutation Del($id: String!) { deleteWebhook(id: $id) }","variables":{"id":"hook-installed"}}`); len(errs) == 0 {
		t.Fatal("expected error from deleteWebhook on post-rename sync failure")
	}

	entries, err := st.ActivityEntriesAfter(context.Background(), 0, []string{store.ActivityConfigUpdate}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].TargetID != "hook-installed" || entries[1].TargetID != "settings" || entries[2].TargetID != "hook-installed" {
		t.Fatalf("activity entries = %+v, want save, settings, & delete recorded", entries)
	}
}

// Without a manager the fields say so rather than failing opaquely.
func TestWebhookFieldsReportUnavailableWithoutManager(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	_, errs := postLibraryTrash(t, handler, `{"query":"query Webhooks { webhooks { id } }"}`)
	if len(errs) == 0 || errs[0] != "webhooks are unavailable" {
		t.Fatalf("errors = %q, want webhooks are unavailable", errs)
	}
}
