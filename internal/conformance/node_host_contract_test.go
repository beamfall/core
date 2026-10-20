package conformance

// ADR-0129 (formalizing ADR-0123 decision 9) node-host contract conformance.
//
// CONF-2 / PENDING NODE-4: wire the three tests below to the real node-host
// transport implementation when the NODE-4 transport ticket lands. There is no
// production node-host transport yet; this file encodes the ADR-0129 contract
// as the normative reference so that, when the impl lands, any drift from the
// contract is caught here. The local types below ARE the spec side.
// See roadmap ticket NODE-4 (node-host transport implementation).
//
// The three contract guarantees asserted (NODE-1 acceptance):
//   1. Start-of-session revision negotiation FAILS CLOSED — an unsupported /
//      mismatched revision drops to refuse-serving / degraded, never proceeds
//      and never silently mis-parses.
//   2. The method/operation vocabulary is the exact closed set, and
//      token-issuance is NOT a member.
//   3. The proxy hot path: the per-node concurrency ceiling is a hard 503
//      reject (not a queue) and the per-stream buffer cap is <= 8 MiB.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── contract constants (the normative / spec side) ──────────────────────────

// maxProxyBufferBytes is the ADR-0129 §5 bounded per-stream buffer cap
// (<= 8 MiB per proxied stream, configurable; 8 MiB is the v1 default ceiling).
const maxProxyBufferBytes = 8 * 1024 * 1024

// nodeHostOperation is a member of the closed, Core-owned operation vocabulary.
type nodeHostOperation string

// The exact closed v1 vocabulary of ADR-0129 §1.
// Nodes never add operations; token-issuance is deliberately absent.
const (
	opIndexPush           nodeHostOperation = "index-push"            // up
	opRequestFileMutation nodeHostOperation = "request-file-mutation" // down
	opProxyByteRange      nodeHostOperation = "proxy-byte-range"      // down
	opPlaybackDecision    nodeHostOperation = "playback-decision"     // down
	opTranscodeRequest    nodeHostOperation = "transcode-request"     // down
	opPurgeTranscodeCache nodeHostOperation = "purge-transcode-cache" // down
	opNodeEnroll          nodeHostOperation = "node-enroll"           // up
	opNodeStatus          nodeHostOperation = "node-status"           // up
	opNodeRevoke          nodeHostOperation = "node-revoke"           // down
	opHealthReport        nodeHostOperation = "health-report"         // up
)

// closedVocabulary is the entire contract surface. It is a closed set: anything
// not present here is, by construction, not a node-host operation.
var closedVocabulary = map[nodeHostOperation]bool{
	opIndexPush:           true,
	opRequestFileMutation: true,
	opProxyByteRange:      true,
	opPlaybackDecision:    true,
	opTranscodeRequest:    true,
	opPurgeTranscodeCache: true,
	opNodeEnroll:          true,
	opNodeStatus:          true,
	opNodeRevoke:          true,
	opHealthReport:        true,
}

type downOperationTransport string

const (
	transportNodeInitiatedMultiplexed downOperationTransport = "node-initiated-multiplexed"
	transportDirectNodeListener       downOperationTransport = "direct-node-listener"
)

// sessionState is the start-of-session negotiation outcome.
type sessionState string

const (
	stateServing     sessionState = "serving"        // agreed revision; may exchange operations
	stateRefuseServe sessionState = "refuse-serving" // fail-closed outcome
	stateDegraded    sessionState = "degraded"       // fail-closed outcome (ADR-0123 decision 10)
)

// supportedRevisions is the main server's set of node-host contract revisions.
var supportedRevisions = map[int]bool{1: true}

// negotiateSession models the ADR-0129 §2 start-of-session negotiation. It
// fails closed: an unsupported / mismatched revision never yields stateServing.
func negotiateSession(nodeRevision int) sessionState {
	if supportedRevisions[nodeRevision] {
		return stateServing
	}
	return stateRefuseServe
}

// proxyAdmit models the ADR-0129 §5 per-node admission ceiling. The ceiling is
// a hard reject (HTTP 503), never a queue: at capacity it returns 503 rather
// than enqueueing.
func proxyAdmit(active, ceiling int) (admitted bool, httpStatus int) {
	if active >= ceiling {
		return false, 503
	}
	return true, 200
}

func downOperationAdmitted(transport downOperationTransport) bool {
	return transport == transportNodeInitiatedMultiplexed
}

// ── assertions ──────────────────────────────────────────────────────────────

// TestNodeHostRevisionNegotiationFailsClosed is the NODE-1 revision-mismatch
// fail-closed corpus: every mismatched revision must drop to refuse-serving /
// degraded and never reach a serving state.
func TestNodeHostRevisionNegotiationFailsClosed(t *testing.T) {
	cases := []struct {
		name         string
		nodeRevision int
		wantServing  bool
	}{
		{"agreed revision serves", 1, true},
		{"unknown future revision fails closed", 2, false},
		{"stale revision fails closed", 0, false},
		{"negative/garbage revision fails closed", -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := negotiateSession(tc.nodeRevision)
			if tc.wantServing {
				if got != stateServing {
					t.Fatalf("revision %d: want serving, got %q", tc.nodeRevision, got)
				}
				return
			}
			// Fail-closed: must be a non-serving state, never serving.
			if got == stateServing {
				t.Fatalf("revision %d: negotiation proceeded to serving; must fail closed (refuse-serving/degraded)", tc.nodeRevision)
			}
			if got != stateRefuseServe && got != stateDegraded {
				t.Fatalf("revision %d: fail-closed outcome must be refuse-serving or degraded, got %q", tc.nodeRevision, got)
			}
		})
	}
}

// TestNodeHostVocabularyIsClosedAndExcludesTokenIssuance asserts the operation
// set is exactly the ADR-0129 §1 closed set and that token issuance is absent.
func TestNodeHostVocabularyIsClosedAndExcludesTokenIssuance(t *testing.T) {
	want := []nodeHostOperation{
		opIndexPush, opRequestFileMutation, opProxyByteRange, opPlaybackDecision,
		opTranscodeRequest, opPurgeTranscodeCache, opNodeEnroll, opNodeStatus,
		opNodeRevoke, opHealthReport,
	}
	if len(closedVocabulary) != len(want) {
		t.Fatalf("vocabulary size %d, want exactly %d (closed set)", len(closedVocabulary), len(want))
	}
	for _, op := range want {
		if !closedVocabulary[op] {
			t.Fatalf("contract is missing required operation %q", op)
		}
	}
	// Token issuance must NOT be a member (ADR-0123 decision 6: tokens stay main-side).
	forbidden := []nodeHostOperation{"issue-stream-token", "issue-token", "stream-token", "mint-token"}
	for _, op := range forbidden {
		if closedVocabulary[op] {
			t.Fatalf("token-issuance operation %q is on the node-host contract; it must be excluded", op)
		}
	}
}

// TestNodeHostProxyCeilingIsHard503NotQueue asserts the per-node admission
// ceiling is a hard 503 reject and the per-stream buffer cap is <= 8 MiB.
func TestNodeHostProxyCeilingIsHard503NotQueue(t *testing.T) {
	const ceiling = 4

	// Below ceiling: admitted.
	if admitted, status := proxyAdmit(ceiling-1, ceiling); !admitted || status != 200 {
		t.Fatalf("below ceiling: want admitted/200, got admitted=%v status=%d", admitted, status)
	}
	// At ceiling: hard 503 reject, NOT a queue (not admitted, not deferred).
	admitted, status := proxyAdmit(ceiling, ceiling)
	if admitted {
		t.Fatalf("at ceiling: stream was admitted; ceiling must be a hard reject, not a queue")
	}
	if status != 503 {
		t.Fatalf("at ceiling: want HTTP 503 hard reject, got %d", status)
	}

	// Per-stream buffer cap must be <= 8 MiB.
	if maxProxyBufferBytes > 8*1024*1024 {
		t.Fatalf("per-stream buffer cap %d exceeds 8 MiB", maxProxyBufferBytes)
	}
}

func TestNodeHostTopologyPinsNodeDialsMain(t *testing.T) {
	if !downOperationAdmitted(transportNodeInitiatedMultiplexed) {
		t.Fatal("down ops over node-initiated multiplexed connection must be admitted")
	}
	if downOperationAdmitted(transportDirectNodeListener) {
		t.Fatal("down ops over a direct node listener must be rejected")
	}
}

func TestNodehostProductionHasNoNodeListener(t *testing.T) {
	dir := filepath.Join("..", "nodehost")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := [][]byte{
		[]byte("net.Listen("),
		[]byte("tls.Listen("),
		[]byte("ListenAndServe("),
		[]byte("ServeTLS("),
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range forbidden {
			if bytes.Contains(src, needle) {
				t.Fatalf("%s contains listener API %q; nodehost must not run a node listener", path, needle)
			}
		}
	}
}
