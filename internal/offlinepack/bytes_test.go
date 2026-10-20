package offlinepack

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"testing"
)

// conformanceVectors is the versioned corpus of shared test-vector inputs and
// expected values for the BFOP/BFKW conformance tests below (loaded from
// testdata/conformance-vectors-v1.json). Extracting these into a shared,
// versioned corpus keeps the golden inputs/outputs reviewable independent of
// the assertion code, and out of scattered in-file constants.
type conformanceVectors struct {
	Version int `json:"version"`
	Vectors struct {
		DeviceKID     deviceKIDVectorGroup     `json:"device_kid"`
		AADWrap       aadWrapVectorGroup       `json:"aad_wrap"`
		BFKWWrap      bfkwWrapVectorGroup      `json:"bfkw_wrap"`
		BFOPHeader    bfopHeaderVectorGroup    `json:"bfop_header"`
		CIKSeparation cikSeparationVectorGroup `json:"cik_separation"`
		BFOPFraming   bfopFramingVectorGroup   `json:"bfop_framing"`
	} `json:"vectors"`
}

type deviceKIDVectorGroup struct {
	LabelPrefixHex    string `json:"label_prefix_hex"`
	TestKeyScalar     int    `json:"test_key_scalar"`
	ExpectedDeviceKID string `json:"expected_device_kid"`
}

type aadWrapVectorGroup struct {
	LabelPrefixHex string `json:"label_prefix_hex"`
	TypeTagHex     string `json:"type_tag_hex"`
	Fields         struct {
		Issuer             string `json:"issuer"`
		PackID             string `json:"pack_id"`
		ProfileID          string `json:"profile_id"`
		DeviceEnrollmentID string `json:"device_enrollment_id"`
		ManifestRevision   string `json:"manifest_revision"`
	} `json:"fields"`
	ManifestSource string `json:"manifest_source"`
	ExpectedAADHex string `json:"expected_aad_hex"`
}

type bfkwWrapVectorGroup struct {
	DeviceKeyScalar        int    `json:"device_key_scalar"`
	EphemeralKeyScalar     int    `json:"ephemeral_key_scalar"`
	OtherDeviceKeyScalar   int    `json:"other_device_key_scalar"`
	RotatedDeviceKeyScalar int    `json:"rotated_device_key_scalar"`
	CEKByteHex             string `json:"cek_byte_hex"`
	CEKLen                 int    `json:"cek_len"`
}

type bfopHeaderVectorGroup struct {
	MagicPrefixHex    string `json:"magic_prefix_hex"`
	ManifestSource    string `json:"manifest_source"`
	Plaintext         string `json:"plaintext"`
	ItemIndex         int    `json:"item_index"`
	ChunkSize         int    `json:"chunk_size"`
	ExpectedHeaderHex string `json:"expected_header_hex"`
}

type cikSeparationVectorGroup struct {
	MagicPrefixHex  string `json:"magic_prefix_hex"`
	CEKByteHex      string `json:"cek_byte_hex"`
	CEKLen          int    `json:"cek_len"`
	ManifestASource string `json:"manifest_a_source"`
	ManifestBSource string `json:"manifest_b_source"`
	ItemIndex       int    `json:"item_index"`
	ExpectedCIKAHex string `json:"expected_cik_a_hex"`
	ExpectedCIKBHex string `json:"expected_cik_b_hex"`
}

type bfopFramingVectorGroup struct {
	CEKByteHex     string `json:"cek_byte_hex"`
	CEKLen         int    `json:"cek_len"`
	ManifestSource string `json:"manifest_source"`
	ItemIndex      int    `json:"item_index"`
	SizeTable      []struct {
		PlaintextLen int `json:"plaintext_len"`
		EncodedSize  int `json:"encoded_size"`
	} `json:"size_table"`
}

// loadConformanceVectors loads the shared vector corpus. A load or parse
// failure fails the test outright (never skips) — the corpus is required
// fixture data, not an optional enhancement.
func loadConformanceVectors(t *testing.T) conformanceVectors {
	t.Helper()
	data, err := os.ReadFile("testdata/conformance-vectors-v1.json")
	if err != nil {
		t.Fatalf("load conformance vector corpus: %v", err)
	}
	var vectors conformanceVectors
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("parse conformance vector corpus: %v", err)
	}
	if vectors.Version != 1 {
		t.Fatalf("conformance vector corpus version = %d, want 1", vectors.Version)
	}
	return vectors
}

func mustHexBytes(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex vector %q: %v", value, err)
	}
	return decoded
}

// vecByte, vecUint32, and vecUint64 bounds-check corpus integers before
// narrowing: a value outside the target range is corrupt fixture data and
// fails the test rather than wrapping silently.
func vecByte(t *testing.T, value int) byte {
	t.Helper()
	if value < 0 || value > math.MaxUint8 {
		t.Fatalf("corpus value %d out of byte range", value)
	}
	return byte(value) //nolint:gosec // bounds-checked against MaxUint8 above
}

func vecUint32(t *testing.T, value int) uint32 {
	t.Helper()
	if value < 0 || value > math.MaxUint32 {
		t.Fatalf("corpus value %d out of uint32 range", value)
	}
	return uint32(value) //nolint:gosec // bounds-checked against MaxUint32 above
}

func vecUint64(t *testing.T, value int) uint64 {
	t.Helper()
	if value < 0 {
		t.Fatalf("corpus value %d out of uint64 range", value)
	}
	return uint64(value) //nolint:gosec // non-negative checked above
}

func TestDeviceKIDVectors(t *testing.T) {
	v := loadConformanceVectors(t).Vectors.DeviceKID
	device := testP256Key(t, vecByte(t, v.TestKeyScalar))
	public := device.PublicKey().Bytes()

	t.Run("W04 label and preimage", func(t *testing.T) {
		got, err := DeviceKID(public)
		if err != nil {
			t.Fatal(err)
		}
		preimage := append(mustHexBytes(t, v.LabelPrefixHex), public...)
		want := sha256.Sum256(preimage)
		if got != base64.RawURLEncoding.EncodeToString(want[:]) || len(got) != 43 {
			t.Fatalf("device_kid = %q", got)
		}
		if got != v.ExpectedDeviceKID {
			t.Fatalf("device_kid = %q, want corpus-pinned %q", got, v.ExpectedDeviceKID)
		}
	})

	for _, tc := range []struct {
		name string
		key  []byte
	}{
		{"W02 compressed", append([]byte{2}, public[1:33]...)},
		{"W02 hybrid", append([]byte{6}, public[1:]...)},
		{"D1 wrong length", public[:64]},
		{"D1 off curve", append([]byte{4}, make([]byte, 64)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DeviceKID(tc.key); err == nil {
				t.Fatal("accepted invalid device public key")
			}
		})
	}
}

func TestAADWrapVectors(t *testing.T) {
	device := testP256Key(t, 1)
	fields := testWrapFields(t, device)

	t.Run("W06 golden bytes", func(t *testing.T) {
		got, err := BuildWrapAAD(fields)
		if err != nil {
			t.Fatal(err)
		}
		values := []string{
			fields.Issuer,
			fields.PackID,
			fields.ProfileID,
			fields.DeviceEnrollmentID,
			fields.DeviceKeyThumbprint,
			fields.ManifestSHA256,
			fields.ManifestRevision,
		}
		v := loadConformanceVectors(t).Vectors.AADWrap
		want := append(mustHexBytes(t, v.LabelPrefixHex), mustHexBytes(t, v.TypeTagHex)...)
		for _, value := range values {
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(len(value))) //nolint:gosec // fixture values are far below 4 GiB
			want = append(want, size[:]...)
			want = append(want, value...)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("AAD_wrap mismatch\n got %x\nwant %x", got, want)
		}
		if wantPinned := mustHexBytes(t, v.ExpectedAADHex); !bytes.Equal(got, wantPinned) {
			t.Fatalf("AAD_wrap mismatch vs corpus-pinned golden\n got %x\nwant %x", got, wantPinned)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*WrapAADFields)
	}{
		{"W08 empty issuer", func(f *WrapAADFields) { f.Issuer = "" }},
		{"W08 empty pack", func(f *WrapAADFields) { f.PackID = "" }},
		{"D6 oversized field", func(f *WrapAADFields) { f.ProfileID = string(make([]byte, 4097)) }},
		{"D6 non-ASCII", func(f *WrapAADFields) { f.ProfileID = "é" }},
		{"D6 padded digest", func(f *WrapAADFields) { f.ManifestSHA256 += "=" }},
		{"D6 leading-zero revision", func(f *WrapAADFields) { f.ManifestRevision = "01" }},
		{"D6 signed revision", func(f *WrapAADFields) { f.ManifestRevision = "-1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := fields
			tc.mutate(&changed)
			if _, err := BuildWrapAAD(changed); err == nil {
				t.Fatal("accepted invalid AAD field")
			}
		})
	}
}

func TestBFKWWrapVectors(t *testing.T) {
	v := loadConformanceVectors(t).Vectors.BFKWWrap
	device := testP256Key(t, vecByte(t, v.DeviceKeyScalar))
	ephemeral := testP256Key(t, vecByte(t, v.EphemeralKeyScalar))
	fields := testWrapFields(t, device)
	aad := mustAAD(t, fields)
	cek := bytes.Repeat(mustHexBytes(t, v.CEKByteHex), v.CEKLen)
	envelope, err := wrapCEKWithEphemeral(device.PublicKey().Bytes(), aad, cek, ephemeral)
	if err != nil {
		t.Fatal(err)
	}
	digest := mustBFKWDigest(t, envelope)

	t.Run("W01 golden round trip", func(t *testing.T) {
		if len(envelope) != BFKWEnvelopeSize {
			t.Fatalf("envelope length = %d", len(envelope))
		}
		encoded := base64.RawURLEncoding.EncodeToString(envelope)
		if len(encoded) != BFKWEncodedSize {
			t.Fatalf("encoded length = %d", len(encoded))
		}
		decoded, err := DecodeWrappedEnvelope(encoded)
		if err != nil || !bytes.Equal(decoded, envelope) {
			t.Fatalf("DecodeWrappedEnvelope: %v", err)
		}
		got, err := UnwrapCEK(device, decoded, aad, fields.DeviceKeyThumbprint, digest)
		if err != nil || !bytes.Equal(got, cek) {
			t.Fatalf("UnwrapCEK = %x, %v", got, err)
		}
	})

	t.Run("W03 invalid ephemeral point", func(t *testing.T) {
		bad := append([]byte(nil), envelope...)
		bad[40] = 4
		clear(bad[41:105])
		if _, err := UnwrapCEK(device, bad, aad, fields.DeviceKeyThumbprint, rawDigest(bad)); err == nil {
			t.Fatal("accepted identity/off-curve ephemeral point")
		}
	})

	t.Run("W05 device kid claim mismatch", func(t *testing.T) {
		otherKID, err := DeviceKID(testP256Key(t, vecByte(t, v.OtherDeviceKeyScalar)).PublicKey().Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := UnwrapCEK(device, envelope, aad, otherKID, digest); err == nil {
			t.Fatal("accepted mismatched device_key_thumbprint")
		}
	})

	t.Run("W07 each AAD field perturbed", func(t *testing.T) {
		otherDigest := sha256.Sum256([]byte("other"))
		otherText := base64.RawURLEncoding.EncodeToString(otherDigest[:])
		mutations := []func(*WrapAADFields){
			func(f *WrapAADFields) { f.Issuer += "x" },
			func(f *WrapAADFields) { f.PackID += "x" },
			func(f *WrapAADFields) { f.ProfileID += "x" },
			func(f *WrapAADFields) { f.DeviceEnrollmentID += "x" },
			func(f *WrapAADFields) { f.DeviceKeyThumbprint = otherText },
			func(f *WrapAADFields) { f.ManifestSHA256 = otherText },
			func(f *WrapAADFields) { f.ManifestRevision = "2" },
		}
		for i, mutate := range mutations {
			changed := fields
			mutate(&changed)
			changedAAD := mustAAD(t, changed)
			if _, err := UnwrapCEK(device, envelope, changedAAD, changed.DeviceKeyThumbprint, digest); err == nil {
				t.Fatalf("accepted perturbed AAD field %d", i)
			}
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"W09 short envelope", func(v []byte) []byte { return v[:len(v)-1] }},
		{"W09 long envelope", func(v []byte) []byte { return append(v, 0) }},
		{"D9 magic", func(v []byte) []byte { v[0] ^= 1; return v }},
		{"D9 version", func(v []byte) []byte { v[4] = 2; return v }},
		{"W10 flags", func(v []byte) []byte { v[6] = 1; return v }},
		{"W10 reserved", func(v []byte) []byte { v[7] = 1; return v }},
		{"W11 suite", func(v []byte) []byte { v[5] = 2; return v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := tc.mutate(append([]byte(nil), envelope...))
			if _, err := ParseBFKW(bad); err == nil {
				t.Fatal("accepted invalid BFKW envelope")
			}
		})
	}

	t.Run("W12 wrap tag tamper", func(t *testing.T) {
		bad := append([]byte(nil), envelope...)
		bad[len(bad)-1] ^= 1
		if _, err := UnwrapCEK(device, bad, aad, fields.DeviceKeyThumbprint, rawDigest(bad)); err == nil {
			t.Fatal("accepted tampered wrapped CEK")
		}
	})

	t.Run("W13 envelope digest mismatch", func(t *testing.T) {
		wrong := sha256.Sum256([]byte("wrong envelope"))
		if _, err := UnwrapCEK(device, envelope, aad, fields.DeviceKeyThumbprint, base64.RawURLEncoding.EncodeToString(wrong[:])); err == nil {
			t.Fatal("accepted mismatched envelope digest")
		}
	})

	t.Run("W14 production wraps use fresh ephemeral keys", func(t *testing.T) {
		first, err := WrapCEK(device.PublicKey().Bytes(), aad, cek)
		if err != nil {
			t.Fatal(err)
		}
		second, err := WrapCEK(device.PublicKey().Bytes(), aad, cek)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(first[40:105], second[40:105]) {
			t.Fatal("production WrapCEK repeated an ephemeral key")
		}
	})

	t.Run("W21 cross-device unwrap", func(t *testing.T) {
		if _, err := UnwrapCEK(testP256Key(t, vecByte(t, v.OtherDeviceKeyScalar)), envelope, aad, fields.DeviceKeyThumbprint, digest); err == nil {
			t.Fatal("another device unwrapped the envelope")
		}
	})

	t.Run("W22 post-rotation old envelope", func(t *testing.T) {
		rotated := testWrapFields(t, testP256Key(t, vecByte(t, v.RotatedDeviceKeyScalar)))
		if _, err := UnwrapCEK(device, envelope, aad, rotated.DeviceKeyThumbprint, digest); err == nil {
			t.Fatal("accepted old envelope after device key rotation")
		}
	})

	t.Run("W26 transcript-derived nonce", func(t *testing.T) {
		shared, err := ephemeral.ECDH(device.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		_, first, err := deriveWrapMaterial(shared, ephemeral.PublicKey().Bytes(), device.PublicKey().Bytes(), aad)
		if err != nil {
			t.Fatal(err)
		}
		_, repeated, err := deriveWrapMaterial(shared, ephemeral.PublicKey().Bytes(), device.PublicKey().Bytes(), aad)
		if err != nil {
			t.Fatal(err)
		}
		changedAAD := append([]byte(nil), aad...)
		changedAAD[len(changedAAD)-1] ^= 1
		_, changed, err := deriveWrapMaterial(shared, ephemeral.PublicKey().Bytes(), device.PublicKey().Bytes(), changedAAD)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, repeated) || bytes.Equal(first, changed) {
			t.Fatal("wrap nonce is not transcript-derived")
		}
	})
}

func TestBFOPHeaderAndBoundsVectors(t *testing.T) {
	v := loadConformanceVectors(t).Vectors.BFOPHeader
	manifest := sha256.Sum256([]byte(v.ManifestSource))
	plaintext := []byte(v.Plaintext)
	itemDigest := sha256.Sum256(plaintext)
	itemIndex := vecUint32(t, v.ItemIndex)

	t.Run("W15 header golden", func(t *testing.T) {
		got, err := makeBFOPHeader(manifest[:], itemDigest[:], itemIndex, uint64(len(plaintext)))
		if err != nil {
			t.Fatal(err)
		}
		want := make([]byte, BFOPHeaderSize)
		copy(want[:8], mustHexBytes(t, v.MagicPrefixHex))
		binary.BigEndian.PutUint32(want[8:12], itemIndex)
		binary.BigEndian.PutUint32(want[12:16], vecUint32(t, v.ChunkSize))
		binary.BigEndian.PutUint64(want[16:24], uint64(len(plaintext)))
		copy(want[24:32], itemDigest[:8])
		copy(want[32:64], manifest[:])
		if !bytes.Equal(got[:], want) {
			t.Fatalf("header mismatch\n got %x\nwant %x", got, want)
		}
		if wantPinned := mustHexBytes(t, v.ExpectedHeaderHex); !bytes.Equal(got[:], wantPinned) {
			t.Fatalf("header mismatch vs corpus-pinned golden\n got %x\nwant %x", got, wantPinned)
		}
	})

	t.Run("W19 plaintext length disagreement", func(t *testing.T) {
		file, err := EncryptPackItem(bytes.Repeat([]byte{1}, 32), manifest[:], itemIndex, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint64(file[16:24], uint64(len(plaintext))+1)
		if err := ValidatePackItem(manifest[:], itemIndex, uint64(len(plaintext)), itemDigest[:], file); err == nil {
			t.Fatal("accepted plaintext_len disagreement")
		}
	})

	for _, version := range []byte{0, 1} {
		t.Run("W24 version reject", func(t *testing.T) {
			file, err := EncryptPackItem(bytes.Repeat([]byte{1}, 32), manifest[:], itemIndex, plaintext)
			if err != nil {
				t.Fatal(err)
			}
			file[4] = version
			if err := ValidatePackItem(manifest[:], itemIndex, uint64(len(plaintext)), itemDigest[:], file); err == nil {
				t.Fatalf("accepted BFOP version %d", version)
			}
		})
	}

	t.Run("D9 fixed header fields", func(t *testing.T) {
		for _, offset := range []int{0, 5, 6, 7, 12} {
			file, err := EncryptPackItem(bytes.Repeat([]byte{1}, 32), manifest[:], itemIndex, plaintext)
			if err != nil {
				t.Fatal(err)
			}
			file[offset] ^= 1
			if err := ValidatePackItem(manifest[:], itemIndex, uint64(len(plaintext)), itemDigest[:], file); err == nil {
				t.Fatalf("accepted header mutation at offset %d", offset)
			}
		}
	})

	t.Run("D9 resource bounds", func(t *testing.T) {
		if err := ValidatePackResourceBounds(make([]uint64, MaxManifestItems)); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePackResourceBounds(make([]uint64, MaxManifestItems+1)); err == nil {
			t.Fatal("accepted too many items")
		}
		if err := ValidateBFOPItemSize(MaxItemSize); err != nil {
			t.Fatal(err)
		}
		if err := ValidateBFOPItemSize(MaxItemSize + 1); err == nil {
			t.Fatal("accepted item larger than 4 GiB")
		}
	})
}

func TestEncryptPackItemToMatchesBufferedEncoding(t *testing.T) {
	cek := bytes.Repeat([]byte{0x31}, sha256.Size)
	manifest := sha256.Sum256([]byte("streaming-manifest"))
	plaintext := makePattern(int(2*BFOPChunkSize + 31))
	digest := sha256.Sum256(plaintext)
	want, err := EncryptPackItem(cek, manifest[:], 7, plaintext)
	if err != nil {
		t.Fatalf("EncryptPackItem: %v", err)
	}
	var got bytes.Buffer
	if err := EncryptPackItemTo(&got, cek, manifest[:], 7, uint64(len(plaintext)), digest[:], bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("EncryptPackItemTo: %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("streamed BFOP bytes differ from buffered encoding")
	}
}

func TestCIKSeparationVectorW23(t *testing.T) {
	v := loadConformanceVectors(t).Vectors.CIKSeparation
	cek := bytes.Repeat(mustHexBytes(t, v.CEKByteHex), v.CEKLen)
	manifestA := sha256.Sum256([]byte(v.ManifestASource))
	manifestB := sha256.Sum256([]byte(v.ManifestBSource))
	cikA, err := DeriveCIK(cek, manifestA[:])
	if err != nil {
		t.Fatal(err)
	}
	cikB, err := DeriveCIK(cek, manifestB[:])
	if err != nil {
		t.Fatal(err)
	}
	if cikA == cikB {
		t.Fatal("different manifest digests derived the same CIK")
	}
	if wantA := mustHexBytes(t, v.ExpectedCIKAHex); !bytes.Equal(cikA[:], wantA) {
		t.Fatalf("cikA = %x, want corpus-pinned %x", cikA, wantA)
	}
	if wantB := mustHexBytes(t, v.ExpectedCIKBHex); !bytes.Equal(cikB[:], wantB) {
		t.Fatalf("cikB = %x, want corpus-pinned %x", cikB, wantB)
	}

	header := make([]byte, BFOPHeaderSize)
	copy(header[:8], mustHexBytes(t, v.MagicPrefixHex))
	nonce, aad := bfopChunkInputs(header, vecUint32(t, v.ItemIndex), 0, true)
	gcmA, err := newGCM(cikA[:])
	if err != nil {
		t.Fatal(err)
	}
	gcmB, err := newGCM(cikB[:])
	if err != nil {
		t.Fatal(err)
	}
	sealed := gcmA.Seal(nil, nonce[:], []byte("same plaintext"), aad[:])
	if _, err := gcmB.Open(nil, nonce[:], sealed, aad[:]); err == nil {
		t.Fatal("revision B CIK authenticated revision A chunk under constant AAD")
	}
}

func TestBFOPFramingVectors(t *testing.T) {
	v := loadConformanceVectors(t).Vectors.BFOPFraming
	cek := bytes.Repeat(mustHexBytes(t, v.CEKByteHex), v.CEKLen)
	manifest := sha256.Sum256([]byte(v.ManifestSource))
	plaintext := makePattern(int(2*BFOPChunkSize + 31))
	itemDigest := sha256.Sum256(plaintext)
	itemIndex := vecUint32(t, v.ItemIndex)
	plaintextLen := uint64(len(plaintext))
	file, err := EncryptPackItem(cek, manifest[:], itemIndex, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("W16 truncation", func(t *testing.T) {
		if _, err := DecryptPackItem(cek, manifest[:], itemIndex, plaintextLen, itemDigest[:], file[:len(file)-1]); err == nil {
			t.Fatal("accepted truncated chunk")
		}
	})

	t.Run("W17 reorder", func(t *testing.T) {
		bad := append([]byte(nil), file...)
		firstOffset, firstLen, _ := BFOPChunkRange(plaintextLen, 0)
		secondOffset, secondLen, _ := BFOPChunkRange(plaintextLen, 1)
		if firstLen != secondLen {
			t.Fatal("test requires equal non-final chunk lengths")
		}
		first := append([]byte(nil), bad[firstOffset:firstOffset+firstLen]...)
		copy(bad[firstOffset:firstOffset+firstLen], bad[secondOffset:secondOffset+secondLen])
		copy(bad[secondOffset:secondOffset+secondLen], first)
		if _, err := DecryptPackItem(cek, manifest[:], itemIndex, plaintextLen, itemDigest[:], bad); err == nil {
			t.Fatal("accepted reordered chunks")
		}
	})

	t.Run("W18 splice from another item", func(t *testing.T) {
		otherFile, err := EncryptPackItem(cek, manifest[:], itemIndex+1, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		bad := append([]byte(nil), file...)
		offset, length, _ := BFOPChunkRange(plaintextLen, 0)
		copy(bad[offset:offset+length], otherFile[offset:offset+length])
		if _, err := DecryptPackItem(cek, manifest[:], itemIndex, plaintextLen, itemDigest[:], bad); err == nil {
			t.Fatal("accepted chunk spliced from another item")
		}
	})

	t.Run("W19 plaintext length disagreement", func(t *testing.T) {
		if _, err := DecryptPackItem(cek, manifest[:], itemIndex, plaintextLen-1, itemDigest[:], file); err == nil {
			t.Fatal("accepted manifest/header plaintext_len disagreement")
		}
	})

	t.Run("W20 chunk index gap or duplicate", func(t *testing.T) {
		bad := append([]byte(nil), file...)
		firstOffset, firstLen, _ := BFOPChunkRange(plaintextLen, 0)
		secondOffset, secondLen, _ := BFOPChunkRange(plaintextLen, 1)
		if firstLen != secondLen {
			t.Fatal("test requires equal non-final chunk lengths")
		}
		copy(bad[secondOffset:secondOffset+secondLen], bad[firstOffset:firstOffset+firstLen])
		if _, err := DecryptPackItem(cek, manifest[:], itemIndex, plaintextLen, itemDigest[:], bad); err == nil {
			t.Fatal("accepted duplicated chunk with missing index")
		}
	})

	t.Run("W25 exact sizes and offsets", func(t *testing.T) {
		for _, tc := range v.SizeTable {
			got, err := BFOPFileSize(vecUint64(t, tc.PlaintextLen))
			if err != nil || got != vecUint64(t, tc.EncodedSize) {
				t.Fatalf("BFOPFileSize(%d) = %d, %v; want %d", tc.PlaintextLen, got, err, tc.EncodedSize)
			}
		}
		offset, length, err := BFOPChunkRange(BFOPChunkSize+1, 1)
		if err != nil || offset != BFOPHeaderSize+BFOPChunkSize+16 || length != 17 {
			t.Fatalf("final range = (%d,%d,%v)", offset, length, err)
		}
		exact := makePattern(int(BFOPChunkSize))
		encoded, err := EncryptPackItem(cek, manifest[:], 0, exact)
		if err != nil || uint64(len(encoded)) != vecUint64(t, v.SizeTable[2].EncodedSize) {
			t.Fatalf("exact-multiple encoding = %d, %v", len(encoded), err)
		}
	})

	t.Run("W28 empty item round trip", func(t *testing.T) {
		encoded, err := EncryptPackItem(cek, manifest[:], 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) != 80 {
			t.Fatalf("empty BFOP length = %d", len(encoded))
		}
		emptyDigest := sha256.Sum256(nil)
		if err := ValidatePackItem(manifest[:], 0, 0, emptyDigest[:], encoded); err != nil {
			t.Fatal(err)
		}
		got, err := DecryptPackItem(cek, manifest[:], 0, 0, emptyDigest[:], encoded)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty decrypt = %x, %v", got, err)
		}
	})
}

func testP256Key(t *testing.T, scalar byte) *ecdh.PrivateKey {
	t.Helper()
	raw := make([]byte, 32)
	raw[31] = scalar
	key, err := ecdh.P256().NewPrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testWrapFields(t *testing.T, device *ecdh.PrivateKey) WrapAADFields {
	t.Helper()
	v := loadConformanceVectors(t).Vectors.AADWrap
	kid, err := DeviceKID(device.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	manifest := sha256.Sum256([]byte(v.ManifestSource))
	return WrapAADFields{
		Issuer:              v.Fields.Issuer,
		PackID:              v.Fields.PackID,
		ProfileID:           v.Fields.ProfileID,
		DeviceEnrollmentID:  v.Fields.DeviceEnrollmentID,
		DeviceKeyThumbprint: kid,
		ManifestSHA256:      base64.RawURLEncoding.EncodeToString(manifest[:]),
		ManifestRevision:    v.Fields.ManifestRevision,
	}
}

func mustAAD(t *testing.T, fields WrapAADFields) []byte {
	t.Helper()
	aad, err := BuildWrapAAD(fields)
	if err != nil {
		t.Fatal(err)
	}
	return aad
}

func mustBFKWDigest(t *testing.T, envelope []byte) string {
	t.Helper()
	digest, err := WrappedEnvelopeSHA256(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func rawDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func makePattern(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}
