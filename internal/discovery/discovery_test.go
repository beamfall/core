package discovery

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var testRecord = Record{Scheme: "http", ServerID: "0123456789abcdef0123456789abcdef"}

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
		if !reflect.DeepEqual(text, []string{"version", "path=/", "scheme=http", "id=0123456789abcdef0123456789abcdef"}) {
			t.Fatalf("text = %#v", text)
		}
		return fake, nil
	})

	if err := advertiser.Start("Beamfall", 8800, testRecord); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := advertiser.Start("Beamfall", 8800, testRecord); err != nil {
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
	got := txtRecords(Record{Scheme: "https", ServerID: "abc123"})
	want := []string{"version", "path=/", "scheme=https", "id=abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("txtRecords = %#v, want %#v", got, want)
	}
	for _, entry := range got {
		if strings.HasPrefix(entry, "setup") {
			t.Fatalf("txt record advertises setup state: %q", entry)
		}
	}
}

func TestServerIDIsStableHexAndNotThePin(t *testing.T) {
	const pin = "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	id := ServerID(pin)
	if id != ServerID(pin) {
		t.Fatal("ServerID is not stable")
	}
	if len(id) != 2*serverIDBytes || !validServerID(id) {
		t.Fatalf("ServerID = %q, want %d lowercase hex characters", id, 2*serverIDBytes)
	}
	if strings.Contains(pin, id) || strings.Contains(id, "47DEQ") {
		t.Fatalf("ServerID %q exposes the pin", id)
	}
	if ServerID(pin+"x") == id {
		t.Fatal("different pins produced the same ServerID")
	}
}

func TestAdvertiserRejectsInvalidInputs(t *testing.T) {
	advertiser := newWithRegister(func(string, string, string, int, []string) (registration, error) {
		return nil, errors.New("should not register")
	})
	for _, tc := range []struct {
		name string
		port int
		rec  Record
	}{
		{"", 8800, testRecord},
		{"Beamfall", 0, testRecord},
		{"Beamfall", 65536, testRecord},
		{"Beamfall", 8800, Record{Scheme: "", ServerID: testRecord.ServerID}},
		{"Beamfall", 8800, Record{Scheme: "ftp", ServerID: testRecord.ServerID}},
		{"Beamfall", 8800, Record{Scheme: "http", ServerID: ""}},
		{"Beamfall", 8800, Record{Scheme: "http", ServerID: "NOT-HEX"}},
		{"Beamfall", 8800, Record{Scheme: "http", ServerID: strings.Repeat("a", 65)}},
	} {
		if err := advertiser.Start(tc.name, tc.port, tc.rec); err == nil {
			t.Fatalf("Start(%q, %d, %+v) succeeded", tc.name, tc.port, tc.rec)
		}
	}
}

type fakeRegistration struct {
	shutdowns int
}

func (f *fakeRegistration) Shutdown() {
	f.shutdowns++
}
