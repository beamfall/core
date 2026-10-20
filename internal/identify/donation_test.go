package identify

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

type donationConsentStub struct {
	grants map[ConsentClass]bool
	calls  []donationConsentCall
}

type donationConsentCall struct {
	class      ConsentClass
	disclosure string
}

func (s *donationConsentStub) ConsentGranted(_ context.Context, class ConsentClass, disclosure string) (bool, error) {
	s.calls = append(s.calls, donationConsentCall{class: class, disclosure: disclosure})
	return s.grants[class], nil
}

type donationDestinationStub struct {
	internal bool
	calls    int
	payload  []byte
}

func (s *donationDestinationStub) InternalOnly() bool { return s.internal }

func (s *donationDestinationStub) SendFixMatchDonation(_ context.Context, payload []byte) error {
	s.calls++
	s.payload = append([]byte(nil), payload...)
	return nil
}

func includedDonationItems() []DonationItem {
	return []DonationItem{
		{
			Include:       true,
			FileNames:     []string{"Example.S01E01.mkv", "Example.S01E01.en.srt"},
			PluginID:      "example.plugin",
			ScraperID:     "catalog",
			CandidateID:   "candidate-1",
			CandidateName: "Example 1",
		},
	}
}

func TestDonationDefaultsOffWithZeroEgress(t *testing.T) {
	consent := &donationConsentStub{grants: map[ConsentClass]bool{}}
	destination := &donationDestinationStub{internal: true}
	service := NewDonationService(consent, destination)
	preview, err := service.Preview(includedDonationItems())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	err = service.Donate(context.Background(), preview, preview.Acknowledgment)
	if !errors.Is(err, ErrDonationConsentRequired) {
		t.Fatalf("Donate error = %v, want ErrDonationConsentRequired", err)
	}
	if destination.calls != 0 {
		t.Fatalf("unconsented donation made %d egress calls, want 0", destination.calls)
	}
	if len(consent.calls) != 1 || consent.calls[0].class != ConsentClassFixMatchDonation {
		t.Fatalf("consent classes checked = %#v, want dedicated fix-match class", consent.calls)
	}
	if consent.calls[0].disclosure != FixMatchDonationDisclosure {
		t.Fatalf("consent disclosure = %q, want %q", consent.calls[0].disclosure, FixMatchDonationDisclosure)
	}
}

func TestDonationDoesNotInheritAnotherConsentClass(t *testing.T) {
	const observabilityConsent ConsentClass = "observability"
	consent := &donationConsentStub{grants: map[ConsentClass]bool{observabilityConsent: true}}
	destination := &donationDestinationStub{internal: true}
	service := NewDonationService(consent, destination)
	preview, err := service.Preview(includedDonationItems())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	err = service.Donate(context.Background(), preview, preview.Acknowledgment)
	if !errors.Is(err, ErrDonationConsentRequired) {
		t.Fatalf("Donate error = %v, want ErrDonationConsentRequired", err)
	}
	if destination.calls != 0 {
		t.Fatalf("wrong-class consent made %d egress calls, want 0", destination.calls)
	}
}

func TestDonationPayloadEqualsPreviewedBytesAndHonorsItemOptOut(t *testing.T) {
	consent := &donationConsentStub{grants: map[ConsentClass]bool{ConsentClassFixMatchDonation: true}}
	destination := &donationDestinationStub{internal: true}
	service := NewDonationService(consent, destination)
	items := append(includedDonationItems(), DonationItem{
		Include:     false,
		FileNames:   []string{"private-opted-out.mkv"},
		PluginID:    "example.plugin",
		ScraperID:   "catalog",
		CandidateID: "candidate-2",
	})
	preview, err := service.Preview(items)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.ItemCount != 1 {
		t.Fatalf("ItemCount = %d, want 1", preview.ItemCount)
	}
	if bytes.Contains(preview.Payload, []byte("private-opted-out.mkv")) {
		t.Fatal("preview contains opted-out item")
	}

	if err := service.Donate(context.Background(), preview, preview.Acknowledgment); err != nil {
		t.Fatalf("Donate: %v", err)
	}
	if destination.calls != 1 {
		t.Fatalf("egress calls = %d, want 1", destination.calls)
	}
	if !bytes.Equal(destination.payload, preview.Payload) {
		t.Fatalf("egress payload = %q, want exact preview bytes %q", destination.payload, preview.Payload)
	}
}

func TestDonationRejectsChangedOrUnacknowledgedPreviewWithZeroEgress(t *testing.T) {
	consent := &donationConsentStub{grants: map[ConsentClass]bool{ConsentClassFixMatchDonation: true}}
	destination := &donationDestinationStub{internal: true}
	service := NewDonationService(consent, destination)
	preview, err := service.Preview(includedDonationItems())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	preview.Payload[0] = '['

	err = service.Donate(context.Background(), preview, preview.Acknowledgment)
	if !errors.Is(err, ErrPreviewAcknowledgment) {
		t.Fatalf("Donate changed preview error = %v, want ErrPreviewAcknowledgment", err)
	}
	if destination.calls != 0 {
		t.Fatalf("changed preview made %d egress calls, want 0", destination.calls)
	}
}

func TestDonationRejectsNonInternalDestination(t *testing.T) {
	consent := &donationConsentStub{grants: map[ConsentClass]bool{ConsentClassFixMatchDonation: true}}
	destination := &donationDestinationStub{internal: false}
	service := NewDonationService(consent, destination)
	preview, err := service.Preview(includedDonationItems())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	err = service.Donate(context.Background(), preview, preview.Acknowledgment)
	if !errors.Is(err, ErrInternalDestination) {
		t.Fatalf("Donate error = %v, want ErrInternalDestination", err)
	}
	if destination.calls != 0 {
		t.Fatalf("non-internal destination made %d calls, want 0", destination.calls)
	}
}

func TestDonationRequiresAtLeastOneIncludedItem(t *testing.T) {
	service := NewDonationService(nil, nil)
	_, err := service.Preview([]DonationItem{{FileNames: []string{"opted-out.mkv"}}})
	if !errors.Is(err, ErrNoDonationItems) {
		t.Fatalf("Preview error = %v, want ErrNoDonationItems", err)
	}
}
