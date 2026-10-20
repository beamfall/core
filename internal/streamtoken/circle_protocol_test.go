package streamtoken

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestSupportedRevisionsRejectsInvalidCurrent(t *testing.T) {
	if _, err := SupportedRevisions(0); !errors.Is(err, ErrRevisionUnsupported) {
		t.Fatalf("SupportedRevisions(0) = %v", err)
	}
	want := []int{CurrentRevision, PreviousRevision}
	if got, err := SupportedRevisions(CurrentRevision); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedRevisions(%d) = %v, %v; want %v", CurrentRevision, got, err, want)
	}
}

func TestCompatibilityMatrix(t *testing.T) {
	cases := []struct {
		owner, guest int
		serve        bool
		revision     int
	}{
		{2, 2, true, 2},
		{2, 1, true, 1},
		{1, 2, true, 1},
		{1, 1, true, 1},
		{3, 1, false, 0},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("owner-%d-guest-%d", tc.owner, tc.guest), func(t *testing.T) {
			got := Negotiate(tc.owner, tc.guest)
			if got.Serve != tc.serve || got.Revision != tc.revision {
				t.Fatalf("Negotiate(%d, %d) = %+v", tc.owner, tc.guest, got)
			}
			if !tc.serve && got.CloseReason != CloseRevisionUnsupported {
				t.Fatalf("refusal reason = %q", got.CloseReason)
			}
		})
	}
}

func TestNegotiateRejectsInvalidPeerRevision(t *testing.T) {
	for _, revisions := range [][2]int{{0, CurrentRevision}, {CurrentRevision, 0}} {
		got := Negotiate(revisions[0], revisions[1])
		if got.Serve || got.CloseReason != CloseRevisionUnsupported {
			t.Fatalf("Negotiate(%d, %d) = %+v", revisions[0], revisions[1], got)
		}
	}
}

func TestSessionFailsClosedBeforeDispatch(t *testing.T) {
	session, err := NewSession("session-1", CurrentRevision)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := 0
	frame := []byte(`{"revision":1,"circleSessionId":"session-1","operation":"catalog-read","kind":"request","requestId":"r1","payload":{"circleId":"c1","remoteProfileId":"p1"}}`)
	if err := session.Accept(frame, func(Frame) error { dispatched++; return nil }); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("Accept mismatch = %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("mismatched frame dispatched %d times", dispatched)
	}
}

func TestSessionAcceptSuccessDispatchErrorAndClosedState(t *testing.T) {
	if _, err := NewSession("", CurrentRevision); !errors.Is(err, ErrProtocol) {
		t.Fatalf("NewSession empty ID = %v", err)
	}
	if _, err := NewSession("session-1", -1); !errors.Is(err, ErrProtocol) {
		t.Fatalf("NewSession negative revision = %v", err)
	}
	session, err := NewSession("session-1", CurrentRevision)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Revision(); got != CurrentRevision {
		t.Fatalf("Revision() = %d", got)
	}
	frame := []byte(`{"revision":2,"circleSessionId":"session-1","operation":"catalog-read","kind":"request","requestId":"r1","payload":{"circleId":"c1"}}`)
	dispatchErr := errors.New("dispatch failed")
	if err := session.Accept(frame, func(got Frame) error {
		if got.RequestID != "r1" || got.Operation != OperationCatalogRead {
			t.Fatalf("dispatched frame = %+v", got)
		}
		return dispatchErr
	}); !errors.Is(err, dispatchErr) {
		t.Fatalf("Accept dispatch error = %v", err)
	}
	if err := session.Accept([]byte(`{`), func(Frame) error { return nil }); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Accept malformed frame = %v", err)
	}
	if err := session.Accept(frame, func(Frame) error { t.Fatal("closed session dispatched"); return nil }); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Accept after close = %v", err)
	}
}

func TestDecodeFrameRejectsUnknownVocabularyAndFields(t *testing.T) {
	cases := [][]byte{
		[]byte(`{`),
		[]byte(`{"revision":-1,"circleSessionId":"s","operation":"catalog-read","kind":"request","requestId":"r","payload":{}}`),
		[]byte(`{"revision":2,"circleSessionId":"","operation":"catalog-read","kind":"request","requestId":"r","payload":{}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"unknown","requestId":"r","payload":{}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"unknown","kind":"request","requestId":"r","payload":{}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"request","requestId":"r","extra":true,"payload":{}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"request","requestId":"r","payload":{"unknown":true}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"session-close","kind":"event","requestId":"r","payload":{"reason":"invented"}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"session-close","kind":"event","requestId":"r","payload":{"reason":1}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"response","requestId":"r","payload":{"refusalCode":"invented"}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"response","requestId":"r","payload":{"refusalCode":1}}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"request","requestId":"r","payload":null}`),
		[]byte(`{"revision":2,"circleSessionId":"s","operation":"catalog-read","kind":"request","requestId":"r","payload":{}} {}`),
	}
	for _, data := range cases {
		if _, err := DecodeFrame(data); err == nil {
			t.Fatalf("DecodeFrame accepted %s", data)
		}
	}
}
