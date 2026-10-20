package graph

import (
	"strings"
	"testing"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSetLibraryBackingAdminGateAndRoundTrip proves the ENC-1 Library backing
// axes are operator-only graph state and round-trip through LibraryInfo.
func TestSetLibraryBackingAdminGateAndRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	body := `{"query":"mutation SetLibraryBacking($libraryID: String!, $custodial: Boolean!, $encrypted: Boolean!) { setLibraryBacking(libraryID: $libraryID, custodial: $custodial, encrypted: $encrypted) { id custodial encrypted } }","variables":{"libraryID":"library-local","custodial":true,"encrypted":true}}`

	_, errs := post(t, handler, body, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin setLibraryBacking errors = %#v, want admin-session gate", errs)
	}

	openManagerAdminSession(t, st)
	data, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setLibraryBacking errors = %#v", errs)
	}
	var changed LibraryInfo
	mustUnmarshal(t, data["setLibraryBacking"], &changed)
	if changed.ID != "library-local" || !changed.Custodial || !changed.Encrypted {
		t.Fatalf("setLibraryBacking = %#v, want custodial+encrypted library-local", changed)
	}

	data, errs = post(t, handler, `{"query":"query { libraries { id custodial encrypted } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraries errors = %#v", errs)
	}
	var libraries []LibraryInfo
	mustUnmarshal(t, data["libraries"], &libraries)
	found := false
	for _, lib := range libraries {
		if lib.ID == "library-local" {
			found = true
			if !lib.Custodial || !lib.Encrypted {
				t.Fatalf("libraries reread library-local = %#v, want custodial+encrypted", lib)
			}
		}
	}
	if !found {
		t.Fatal("library-local missing from libraries reread")
	}
}

// TestSetLibraryBackingValidationCoversRequiredBooleansAndProvisionedEncrypted
// guards the operator contract around the two explicit axes and prevents an
// ENC-2 provisioned Library from being flagged non-encrypted while key state
// still exists.
func TestSetLibraryBackingValidationCoversRequiredBooleansAndProvisionedEncrypted(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.SetLibEncrypt(libencrypt.NewManagerWithParams(newMemSecretStore(), libencrypt.TestArgon2Params))
	openManagerAdminSession(t, st)

	_, errs := post(t, handler, `{"query":"mutation SetLibraryBacking($libraryID: String!, $custodial: Boolean!) { setLibraryBacking(libraryID: $libraryID, custodial: $custodial) { id } }","variables":{"libraryID":"library-local","custodial":true}}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "encrypted is required") {
		t.Fatalf("missing encrypted errors = %#v, want required boolean", errs)
	}

	_, errs = post(t, handler, `{"query":"mutation SetLibraryBacking($libraryID: String!, $encrypted: Boolean!) { setLibraryBacking(libraryID: $libraryID, encrypted: $encrypted) { id } }","variables":{"libraryID":"library-local","encrypted":true}}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "custodial is required") {
		t.Fatalf("missing custodial errors = %#v, want required boolean", errs)
	}

	_, errs = post(t, handler, `{"query":"mutation SetLibraryBacking($libraryID: String!, $custodial: Boolean!, $encrypted: Boolean!) { setLibraryBacking(libraryID: $libraryID, custodial: $custodial, encrypted: $encrypted) { id } }","variables":{"libraryID":"no-such-library","custodial":true,"encrypted":true}}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown library") {
		t.Fatalf("unknown library errors = %#v, want unknown library", errs)
	}

	libID := newEncryptedLibrary(t, st)
	provision := `mutation P($libraryID: ID!, $unsealMode: String) { provisionEncryptedLibrary(libraryID: $libraryID, unsealMode: $unsealMode) { recoveryKey } }`
	if _, errs := post(t, handler, `{"query":"`+provision+`","variables":{"libraryID":"`+libID+`","unsealMode":"auto"}}`, authorizeManager); len(errs) != 0 {
		t.Fatalf("provisionEncryptedLibrary errors = %#v", errs)
	}

	disable := `{"query":"mutation SetLibraryBacking($libraryID: String!, $custodial: Boolean!, $encrypted: Boolean!) { setLibraryBacking(libraryID: $libraryID, custodial: $custodial, encrypted: $encrypted) { id } }","variables":{"libraryID":"` + libID + `","custodial":true,"encrypted":false}}`
	_, errs = post(t, handler, disable, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "encrypted library is already provisioned") {
		t.Fatalf("disable provisioned encrypted errors = %#v, want provisioned guard", errs)
	}

	libraries, err := st.Libraries(t.Context(), store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range libraries {
		if string(lib.ID) == libID && !lib.Encrypted {
			t.Fatalf("provisioned library encrypted flag was cleared: %#v", lib)
		}
	}
}
