package discovery

import (
	"errors"
	"reflect"
	"testing"
)

func TestAdvertiserLifecycleIsIdempotent(t *testing.T) {
	fake := &fakeRegistration{}
	calls := 0
	advertiser := newWithRegister(func(instance, service, gotDomain string, port int, text []string) (registration, error) {
		calls++
		if instance != "Beamfall" {
			t.Fatalf("instance = %q, want Beamfall", instance)
		}
		if service != serviceType {
			t.Fatalf("service = %q, want %q", service, serviceType)
		}
		if gotDomain != domain {
			t.Fatalf("domain = %q, want %q", gotDomain, domain)
		}
		if port != 8800 {
			t.Fatalf("port = %d, want 8800", port)
		}
		if !reflect.DeepEqual(text, []string{"version", "path=/"}) {
			t.Fatalf("text = %#v", text)
		}
		return fake, nil
	})

	if err := advertiser.Start("Beamfall", 8800); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := advertiser.Start("Beamfall", 8800); err != nil {
		t.Fatalf("second Start returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("register calls = %d, want 1", calls)
	}

	advertiser.Stop()
	advertiser.Stop()
	if fake.shutdowns != 1 {
		t.Fatalf("shutdowns = %d, want 1", fake.shutdowns)
	}
}

func TestAdvertiserTXTRecordsArePinned(t *testing.T) {
	got := txtRecords()
	want := []string{"version", "path=/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("txtRecords = %#v, want %#v", got, want)
	}
}

func TestAdvertiserRejectsInvalidInputs(t *testing.T) {
	advertiser := newWithRegister(func(string, string, string, int, []string) (registration, error) {
		return nil, errors.New("should not register")
	})
	for _, tc := range []struct {
		name string
		port int
	}{
		{"", 8800},
		{"Beamfall", 0},
		{"Beamfall", 65536},
	} {
		if err := advertiser.Start(tc.name, tc.port); err == nil {
			t.Fatalf("Start(%q, %d) succeeded", tc.name, tc.port)
		}
	}
}

type fakeRegistration struct {
	shutdowns int
}

func (f *fakeRegistration) Shutdown() {
	f.shutdowns++
}
