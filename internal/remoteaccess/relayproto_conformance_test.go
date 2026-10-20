// relayproto_conformance_test.go verifies that the core relay protocol
// implementation (relayproto.go) round-trips each golden frame vector exactly.
// The vectors are published by the beamfall-relay repo
// and copied here at internal/remoteaccess/testdata/relay_vectors_v1.json so
// the core gate is self-contained.
//
// A json-tag rename, field addition, or encoding change on either side of the
// wire contract causes an immediate test failure here.
//
// To update vectors: regenerate from the relay repo
//
//	cd beamfall-relay && make gen-vectors
//
// then copy artifacts/protocol/v1/vectors.json to
//
//	beamfall/internal/remoteaccess/testdata/relay_vectors_v1.json
package remoteaccess

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// conformanceVectorFile matches the schema of relay_vectors_v1.json.
type conformanceVectorFile struct {
	SchemaVersion int                 `json:"schema_version"`
	Carriers      []string            `json:"carriers"`
	Vectors       []conformanceVector `json:"vectors"`
}

// conformanceVector holds one golden test case. The Struct field mirrors the
// relay's protocol.Frame JSON shape, decoded into anonymous fields so this file
// imports no relay package.
type conformanceVector struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Struct       map[string]interface{} `json:"struct"`
	WireBytesHex string                 `json:"wire_bytes_hex"`
}

// conformanceVectorsPath returns the path to the committed vectors file. Go
// test CWD is the package source directory.
func conformanceVectorsPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// Try RELAY_VECTORS_PATH env override first (for local cross-repo runs).
	if p := os.Getenv("RELAY_VECTORS_PATH"); p != "" {
		return p
	}
	// Default: committed copy alongside this file.
	return filepath.Join(filepath.Dir(file), "testdata", "relay_vectors_v1.json")
}

func loadConformanceVectors(t *testing.T) conformanceVectorFile {
	t.Helper()
	path := conformanceVectorsPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load relay golden vectors %s: %v", path, err)
	}
	var vf conformanceVectorFile
	if err := json.Unmarshal(data, &vf); err != nil {
		t.Fatalf("parse relay golden vectors: %v", err)
	}
	wantCarriers := map[string]bool{"tcp": true, "quic": true}
	for _, carrier := range vf.Carriers {
		delete(wantCarriers, carrier)
	}
	if len(wantCarriers) != 0 {
		t.Fatalf("relay golden vectors missing carrier coverage: %v", wantCarriers)
	}
	return vf
}

// TestRelayProtoConformanceDecode verifies readRelayFrame produces the expected
// JSON-equivalent struct for every golden wire vector.
func TestRelayProtoConformanceDecode(t *testing.T) {
	vf := loadConformanceVectors(t)
	for _, v := range vf.Vectors {
		v := v
		t.Run(v.Name+"/decode", func(t *testing.T) {
			wire, err := hex.DecodeString(v.WireBytesHex)
			if err != nil {
				t.Fatalf("decode hex: %v", err)
			}
			got, err := readRelayFrame(bytes.NewReader(wire))
			if err != nil {
				t.Fatalf("readRelayFrame: %v", err)
			}
			// Canonicalise both sides through JSON so struct tags are exercised.
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal decoded: %v", err)
			}
			wantJSON, err := json.Marshal(v.Struct)
			if err != nil {
				t.Fatalf("marshal golden struct: %v", err)
			}
			// Normalise via round-trip to map so numeric types align.
			var gotMap, wantMap map[string]interface{}
			_ = json.Unmarshal(gotJSON, &gotMap)
			_ = json.Unmarshal(wantJSON, &wantMap)
			gotNorm, _ := json.Marshal(gotMap)
			wantNorm, _ := json.Marshal(wantMap)
			if !bytes.Equal(gotNorm, wantNorm) {
				t.Errorf("decoded struct mismatch:\n  got  %s\n  want %s", gotNorm, wantNorm)
			}
		})
	}
}

// TestRelayProtoConformanceEncode verifies writeRelayFrame produces the exact
// golden wire bytes for each golden struct. Any json-tag drift causes failure.
func TestRelayProtoConformanceEncode(t *testing.T) {
	vf := loadConformanceVectors(t)
	for _, v := range vf.Vectors {
		v := v
		t.Run(v.Name+"/encode", func(t *testing.T) {
			want, err := hex.DecodeString(v.WireBytesHex)
			if err != nil {
				t.Fatalf("decode hex: %v", err)
			}

			// Reconstruct the relayFrame from the vector's JSON struct.
			// We marshal the map back to JSON and unmarshal into relayFrame.
			structJSON, err := json.Marshal(v.Struct)
			if err != nil {
				t.Fatalf("marshal struct: %v", err)
			}
			var frame relayFrame
			if err := json.Unmarshal(structJSON, &frame); err != nil {
				t.Fatalf("unmarshal into relayFrame: %v", err)
			}

			var buf bytes.Buffer
			if err := writeRelayFrame(&buf, frame); err != nil {
				t.Fatalf("writeRelayFrame: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("encoded bytes mismatch:\n  got  %x\n  want %x", buf.Bytes(), want)
			}
		})
	}
}
