package graph

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/jsonstate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/webhook"
)

// Console webhook management (CORE-1697). Owners add, edit, test and remove
// outbound notification endpoints and read the delivery log. Every field here
// is Admin-Session gated in execute(). No secret crosses the wire outward:
// endpoint URLs come back redacted and a stored secret is reported only as
// set or unset.

// WebhookAdmin is the webhook manager seam the console fields drive.
type WebhookAdmin interface {
	Endpoints() []webhook.EndpointView
	SaveEndpoint(webhook.EndpointInput) (webhook.EndpointView, error)
	DeleteEndpoint(id string) error
	TestEndpoint(ctx context.Context, id string) (webhook.TestResult, error)
	Deliveries() []webhook.Delivery
	Settings() webhook.Settings
	SetSettings(webhook.Settings) (webhook.Settings, error)
}

// SetWebhooks wires the webhook manager behind the console fields.
func (h *Handler) SetWebhooks(admin WebhookAdmin) {
	h.webhooks = admin
}

// WebhookEndpointInfo is one configured endpoint on the wire.
type WebhookEndpointInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Format string `json:"format"`
	// URL is redacted: scheme and host only, never the path or query a chat
	// service puts its token in.
	URL                 string   `json:"url"`
	EventKinds          []string `json:"eventKinds"`
	Disabled            bool     `json:"disabled"`
	AllowPrivateNetwork bool     `json:"allowPrivateNetwork"`
	// SecretSet reports whether a signing key, token or password is stored.
	SecretSet bool     `json:"secretSet"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	Username  string   `json:"username"`
	// Problem is empty for a running endpoint, else why it cannot run.
	Problem string `json:"problem"`
}

// WebhookTestResultInfo is the outcome of one test send.
type WebhookTestResultInfo struct {
	OK          bool   `json:"ok"`
	StatusClass string `json:"statusClass"`
	Diagnostic  string `json:"diagnostic"`
	DurationMs  int    `json:"durationMs"`
}

// WebhookDeliveryInfo is one delivery log row.
type WebhookDeliveryInfo struct {
	EndpointID  string `json:"endpointID"`
	EventKind   string `json:"eventKind"`
	Outcome     string `json:"outcome"`
	StatusClass string `json:"statusClass"`
	Attempts    int    `json:"attempts"`
	DurationMs  int    `json:"durationMs"`
	At          string `json:"at"`
	Test        bool   `json:"test"`
}

// WebhookSettingsInfo carries the shared options plus the choices the console
// offers when adding an endpoint.
type WebhookSettingsInfo struct {
	PublicBaseURL string   `json:"publicBaseURL"`
	Formats       []string `json:"formats"`
	EventKinds    []string `json:"eventKinds"`
}

// webhookFormats is the console order of supported formats.
var webhookFormats = []string{
	string(webhook.FormatDiscord), string(webhook.FormatSlack), string(webhook.FormatNtfy),
	string(webhook.FormatGotify), string(webhook.FormatEmail), string(webhook.FormatGeneric),
}

func (h *Handler) webhookAdmin() (WebhookAdmin, error) {
	if h.webhooks == nil {
		return nil, clientError("webhooks are unavailable")
	}
	return h.webhooks, nil
}

// webhookError turns a manager failure into its fixed console text. A
// diagnostic carries no URL, secret or path, so it is safe to display.
func webhookError(err error) error {
	var diag webhook.DiagnosticError
	if errors.As(err, &diag) {
		return clientError(diag.Error())
	}
	return err
}

func webhookEndpointInfo(view webhook.EndpointView) WebhookEndpointInfo {
	kinds := view.EventKinds
	if kinds == nil {
		kinds = []string{}
	}
	to := view.To
	if to == nil {
		to = []string{}
	}
	return WebhookEndpointInfo{
		ID:                  view.ID,
		Name:                view.Name,
		Format:              string(view.Format),
		URL:                 view.URL,
		EventKinds:          kinds,
		Disabled:            view.Disabled,
		AllowPrivateNetwork: view.AllowPrivateNetwork,
		SecretSet:           view.SecretSet,
		From:                view.From,
		To:                  to,
		Username:            view.Username,
		Problem:             view.Problem,
	}
}

func webhookSettingsInfo(s webhook.Settings) WebhookSettingsInfo {
	return WebhookSettingsInfo{
		PublicBaseURL: s.PublicBaseURL,
		Formats:       append([]string(nil), webhookFormats...),
		EventKinds:    append([]string(nil), webhook.DerivedKinds...),
	}
}

// webhookEndpoints lists the configured endpoints.
func (h *Handler) webhookEndpoints() ([]WebhookEndpointInfo, error) {
	admin, err := h.webhookAdmin()
	if err != nil {
		return nil, err
	}
	views := admin.Endpoints()
	resp := make([]WebhookEndpointInfo, len(views))
	for i, view := range views {
		resp[i] = webhookEndpointInfo(view)
	}
	return resp, nil
}

// saveWebhook creates an endpoint (no id) or replaces one. An absent url
// keeps the saved one; an absent secret keeps the saved secret and an empty
// one clears it.
func (h *Handler) saveWebhook(ctx context.Context, scope auth.Scope, req request) (WebhookEndpointInfo, error) {
	admin, err := h.webhookAdmin()
	if err != nil {
		return WebhookEndpointInfo{}, err
	}
	v := newVars(req)
	in := webhook.EndpointInput{}
	in.ID, _ = v.String("id")
	in.Name, _ = v.String("name")
	in.Format, _ = v.String("format")
	in.URL, _ = v.String("url")
	in.EventKinds, _ = v.StringList("eventKinds", "event kinds")
	in.Disabled, _ = v.Bool("disabled")
	in.AllowPrivateNetwork, _ = v.Bool("allowPrivateNetwork")
	if secret, ok := v.String("secret"); ok {
		in.Secret = &secret
	}
	in.From, _ = v.String("from")
	in.To, _ = v.StringList("to", "email addresses")
	in.Username, _ = v.String("username")
	if err := v.Err(); err != nil {
		return WebhookEndpointInfo{}, err
	}
	view, err := admin.SaveEndpoint(in)
	if err != nil && view.ID == "" {
		return WebhookEndpointInfo{}, webhookError(err)
	}
	if logErr := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "webhook", view.ID, ""); logErr != nil {
		return WebhookEndpointInfo{}, logErr
	}
	if err != nil {
		return WebhookEndpointInfo{}, webhookError(err)
	}
	return webhookEndpointInfo(view), nil
}

// deleteWebhook removes an endpoint and its stored secret.
func (h *Handler) deleteWebhook(ctx context.Context, scope auth.Scope, req request) error {
	admin, err := h.webhookAdmin()
	if err != nil {
		return err
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return clientError("id is required")
	}
	delErr := admin.DeleteEndpoint(id)
	var postSync *jsonstate.PostRenameSyncError
	if delErr != nil && !errors.As(delErr, &postSync) {
		return webhookError(delErr)
	}
	if logErr := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "webhook", id, ""); logErr != nil {
		return logErr
	}
	if delErr != nil {
		return webhookError(delErr)
	}
	return nil
}

// testWebhook sends one synthetic notification and reports the outcome. A
// failed delivery is a result, not an error.
func (h *Handler) testWebhook(ctx context.Context, req request) (WebhookTestResultInfo, error) {
	admin, err := h.webhookAdmin()
	if err != nil {
		return WebhookTestResultInfo{}, err
	}
	id := trimmedVariable(req, "id")
	if id == "" {
		return WebhookTestResultInfo{}, clientError("id is required")
	}
	result, err := admin.TestEndpoint(ctx, id)
	if err != nil {
		return WebhookTestResultInfo{}, webhookError(err)
	}
	return WebhookTestResultInfo{
		OK:          result.OK,
		StatusClass: result.StatusClass,
		Diagnostic:  result.Diagnostic,
		DurationMs:  int(result.DurationMs),
	}, nil
}

// webhookDeliveries returns the delivery log, newest first.
func (h *Handler) webhookDeliveries() ([]WebhookDeliveryInfo, error) {
	admin, err := h.webhookAdmin()
	if err != nil {
		return nil, err
	}
	log := admin.Deliveries()
	resp := make([]WebhookDeliveryInfo, len(log))
	for i, d := range log {
		resp[i] = WebhookDeliveryInfo{
			EndpointID:  d.EndpointID,
			EventKind:   d.EventKind,
			Outcome:     d.Outcome,
			StatusClass: d.StatusClass,
			Attempts:    d.Attempts,
			DurationMs:  int(d.DurationMs),
			At:          d.At.UTC().Format(time.RFC3339),
			Test:        d.Test,
		}
	}
	return resp, nil
}

// webhookSettings returns the shared options and the console's choices.
func (h *Handler) webhookSettings() (WebhookSettingsInfo, error) {
	admin, err := h.webhookAdmin()
	if err != nil {
		return WebhookSettingsInfo{}, err
	}
	return webhookSettingsInfo(admin.Settings()), nil
}

// setWebhookSettings saves the shared options.
func (h *Handler) setWebhookSettings(ctx context.Context, scope auth.Scope, req request) (WebhookSettingsInfo, error) {
	admin, err := h.webhookAdmin()
	if err != nil {
		return WebhookSettingsInfo{}, err
	}
	v := newVars(req)
	base, _ := v.String("publicBaseURL")
	if err := v.Err(); err != nil {
		return WebhookSettingsInfo{}, err
	}
	saved, saveErr := admin.SetSettings(webhook.Settings{PublicBaseURL: base})
	var postSync *jsonstate.PostRenameSyncError
	if saveErr != nil && !errors.As(saveErr, &postSync) {
		return WebhookSettingsInfo{}, webhookError(saveErr)
	}
	if logErr := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "webhook", "settings", ""); logErr != nil {
		return WebhookSettingsInfo{}, logErr
	}
	if saveErr != nil {
		return WebhookSettingsInfo{}, webhookError(saveErr)
	}
	return webhookSettingsInfo(saved), nil
}
