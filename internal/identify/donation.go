package identify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// FixMatchDonationDisclosure is the operator-facing disclosure bound to the
// dedicated donation consent class. It deliberately describes
// filenames as disclosed data rather than calling the payload anonymous.
const FixMatchDonationDisclosure = "share the names of these files with Beamfall"

// ConsentClass is a closed core consent vocabulary. Fix-match donation is
// intentionally separate from observability: enabling metrics or diagnostics
// can never satisfy this class.
type ConsentClass string

const ConsentClassFixMatchDonation ConsentClass = "fix-match-donation.explicit-disclosure.v1"

var (
	ErrDonationConsentRequired = errors.New("identify: explicit fix-match donation consent is required")
	ErrPreviewAcknowledgment   = errors.New("identify: exact donation preview acknowledgment is required")
	ErrInternalDestination     = errors.New("identify: donation destination must be internal-only")
	ErrNoDonationItems         = errors.New("identify: donation preview has no included items")
)

// ConsentStore owns consent independently from observability configuration.
// Implementations must default to false when the class has no explicit grant.
type ConsentStore interface {
	ConsentGranted(ctx context.Context, class ConsentClass, disclosure string) (bool, error)
}

// DonationDestination accepts internal training fixtures. Public/open-dataset
// publication is intentionally not represented: ADR-0162 requires a second,
// separate consent before any such destination can exist.
type DonationDestination interface {
	InternalOnly() bool
	SendFixMatchDonation(ctx context.Context, payload []byte) error
}

// DonationItem is one manual correction offered for donation. Include is an
// explicit per-item choice and defaults false, giving every item an opt-out.
// FileNames are payload data and must already be names, not filesystem paths.
type DonationItem struct {
	Include       bool
	FileNames     []string
	PluginID      string
	ScraperID     string
	CandidateID   string
	CandidateName string
}

type donationPayload struct {
	Version int                   `json:"version"`
	Items   []donationPayloadItem `json:"items"`
}

type donationPayloadItem struct {
	FileNames     []string `json:"file_names"`
	PluginID      string   `json:"plugin_id"`
	ScraperID     string   `json:"scraper_id"`
	CandidateID   string   `json:"candidate_id"`
	CandidateName string   `json:"candidate_name,omitempty"`
}

// DonationPreview is the exact egress body plus its acknowledgment token.
// Payload must be rendered verbatim to the operator; Acknowledgment is returned
// unchanged when they approve that preview.
type DonationPreview struct {
	Payload        []byte
	Acknowledgment string
	ItemCount      int
}

// DonationService builds and submits privacy-gated fix-match fixtures.
type DonationService struct {
	consent     ConsentStore
	destination DonationDestination
}

func NewDonationService(consent ConsentStore, destination DonationDestination) *DonationService {
	return &DonationService{consent: consent, destination: destination}
}

// Preview serializes exactly the explicitly included items. It performs no
// egress and does not require consent so the operator can inspect the payload
// before choosing whether to grant the dedicated class.
func (s *DonationService) Preview(items []DonationItem) (DonationPreview, error) {
	payloadItems := make([]donationPayloadItem, 0, len(items))
	for _, item := range items {
		if !item.Include {
			continue
		}
		fileNames := make([]string, 0, len(item.FileNames))
		for _, name := range item.FileNames {
			name = strings.TrimSpace(name)
			if name != "" {
				fileNames = append(fileNames, name)
			}
		}
		payloadItems = append(payloadItems, donationPayloadItem{
			FileNames:     fileNames,
			PluginID:      strings.TrimSpace(item.PluginID),
			ScraperID:     strings.TrimSpace(item.ScraperID),
			CandidateID:   strings.TrimSpace(item.CandidateID),
			CandidateName: strings.TrimSpace(item.CandidateName),
		})
	}
	if len(payloadItems) == 0 {
		return DonationPreview{}, ErrNoDonationItems
	}
	payload, err := json.Marshal(donationPayload{Version: 1, Items: payloadItems})
	if err != nil {
		return DonationPreview{}, err
	}
	digest := sha256.Sum256(payload)
	return DonationPreview{
		Payload:        payload,
		Acknowledgment: hex.EncodeToString(digest[:]),
		ItemCount:      len(payloadItems),
	}, nil
}

// Donate sends the previewed bytes verbatim after rechecking every boundary at
// send time. A changed payload invalidates the acknowledgment and causes zero
// egress, preventing preview/submit time-of-check-time-of-use drift.
func (s *DonationService) Donate(ctx context.Context, preview DonationPreview, acknowledgment string) error {
	if s == nil || s.consent == nil {
		return ErrDonationConsentRequired
	}
	granted, err := s.consent.ConsentGranted(ctx, ConsentClassFixMatchDonation, FixMatchDonationDisclosure)
	if err != nil {
		return err
	}
	if !granted {
		return ErrDonationConsentRequired
	}
	if s.destination == nil || !s.destination.InternalOnly() {
		return ErrInternalDestination
	}
	digest := sha256.Sum256(preview.Payload)
	want := hex.EncodeToString(digest[:])
	if acknowledgment == "" || acknowledgment != preview.Acknowledgment || acknowledgment != want {
		return ErrPreviewAcknowledgment
	}
	if preview.ItemCount < 1 || len(preview.Payload) == 0 {
		return ErrNoDonationItems
	}
	return s.destination.SendFixMatchDonation(ctx, append([]byte(nil), preview.Payload...))
}
