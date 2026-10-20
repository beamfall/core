package wrapvectors

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

var vectorSemantics = []struct {
	description string
	expected    string
}{
	{"golden BFKW wrap and unwrap round trip", "accept"},
	{"compressed device point", "reject"},
	{"off-curve or identity ephemeral point", "reject"},
	{"device_kid preimage and label", "accept"},
	{"device_kid mismatch against grant claim", "reject"},
	{"AAD_wrap golden bytes", "accept"},
	{"each of the seven AAD_wrap fields perturbed", "reject"},
	{"zero-length AAD_wrap field", "reject"},
	{"BFKW envelope wrong length", "reject"},
	{"non-zero BFKW flags or reserved byte", "reject"},
	{"unknown BFKW suite", "reject"},
	{"wrapped CEK tag tamper", "reject"},
	{"BFKW envelope digest mismatch", "reject"},
	{"repeated ephemeral key pair across two wraps", "reject"},
	{"BFOP header golden bytes", "accept"},
	{"BFOP chunk truncation", "reject"},
	{"BFOP chunk reorder", "reject"},
	{"BFOP chunk splice from another item", "reject"},
	{"BFOP plaintext_len disagreement", "reject"},
	{"BFOP chunk-index gap or duplicate", "reject"},
	{"cross-device BFKW unwrap", "reject"},
	{"post-rotation old BFKW envelope", "reject"},
	{"cross-revision CIK separation under constant AAD", "reject"},
	{"BFOP version 0x00 and 0x01", "reject"},
	{"BFOP on-disk lengths and offsets", "accept"},
	{"transcript-derived BFKW nonce", "accept"},
	{"invalid device-key attestation classes", "reject"},
	{"empty BFOP item round trip and 80-byte size", "accept"},
}

func Generate() ([]byte, error) {
	corpus := Corpus{
		SchemaVersion:   1,
		Artifact:        ArtifactName,
		ArtifactVersion: ArtifactVersion,
		Contract:        "ADR-0208",
		Vectors:         make([]Vector, VectorCount),
	}
	for index, semantics := range vectorSemantics {
		id := fmt.Sprintf("W%02d", index+1)
		transcript, err := makeTranscript(index + 1)
		if err != nil {
			return nil, fmt.Errorf("%s transcript: %w", id, err)
		}
		corpus.Vectors[index] = Vector{
			ID:          id,
			Description: semantics.description,
			Expected:    semantics.expected,
			Transcripts: []WrapTranscript{transcript},
			Fields:      map[string]string{},
			Lists:       map[string][]string{},
		}
	}
	reused, err := makeReusedTranscript(corpus.Vectors[13].Transcripts[0])
	if err != nil {
		return nil, fmt.Errorf("W14 reused transcript: %w", err)
	}
	corpus.Vectors[13].Transcripts = append(corpus.Vectors[13].Transcripts, reused)
	for index := range corpus.Vectors {
		if err := populateVector(&corpus.Vectors[index]); err != nil {
			return nil, fmt.Errorf("%s: %w", corpus.Vectors[index].ID, err)
		}
		if len(corpus.Vectors[index].Fields) == 0 {
			corpus.Vectors[index].Fields = nil
		}
		if len(corpus.Vectors[index].Lists) == 0 {
			corpus.Vectors[index].Lists = nil
		}
	}
	if err := ValidateTupleRule(corpus); err != nil {
		return nil, err
	}
	if err := ValidateCoverage(corpus); err != nil {
		return nil, err
	}
	return Encode(corpus)
}

func makeReusedTranscript(first WrapTranscript) (WrapTranscript, error) {
	devicePublic, err := decodeHex(first.DevicePublicKeyHex)
	if err != nil {
		return WrapTranscript{}, err
	}
	ephemeral, err := ecdhPrivate(first.EphemeralPrivateKeyHex)
	if err != nil {
		return WrapTranscript{}, err
	}
	aad, err := decodeHex(first.AADWrapHex)
	if err != nil {
		return WrapTranscript{}, err
	}
	cek := sha256.Sum256([]byte("Beamfall ADR-0208 W14 deterministic reused-transcript CEK"))
	envelope, err := wrap(devicePublic, aad, cek[:], ephemeral)
	if err != nil {
		return WrapTranscript{}, err
	}
	second := first
	second.CEKHex = bytesHex(cek[:])
	second.EnvelopeHex = bytesHex(envelope)
	return second, nil
}

func makeTranscript(index int) (WrapTranscript, error) {
	if index < 1 {
		return WrapTranscript{}, fmt.Errorf("transcript index must be positive")
	}
	if index > math.MaxUint8/2 {
		return WrapTranscript{}, fmt.Errorf("transcript index %d exceeds scalar range", index)
	}
	id := fmt.Sprintf("W%02d", index)
	device, err := scalarKey(byte(index*2 - 1))
	if err != nil {
		return WrapTranscript{}, err
	}
	ephemeral, err := scalarKey(byte(index * 2))
	if err != nil {
		return WrapTranscript{}, err
	}
	fields, err := aadFor(id, index, device.PublicKey().Bytes())
	if err != nil {
		return WrapTranscript{}, err
	}
	aad, err := buildAAD(fields)
	if err != nil {
		return WrapTranscript{}, err
	}
	cek := sha256.Sum256([]byte("Beamfall ADR-0208 " + id + " deterministic CEK"))
	envelope, err := wrap(device.PublicKey().Bytes(), aad, cek[:], ephemeral)
	if err != nil {
		return WrapTranscript{}, err
	}
	return WrapTranscript{
		DevicePrivateKeyHex:    keyHex(device),
		DevicePublicKeyHex:     bytesHex(device.PublicKey().Bytes()),
		EphemeralPrivateKeyHex: keyHex(ephemeral),
		EphemeralPublicKeyHex:  bytesHex(ephemeral.PublicKey().Bytes()),
		AADWrapHex:             bytesHex(aad),
		CEKHex:                 bytesHex(cek[:]),
		EnvelopeHex:            bytesHex(envelope),
	}, nil
}

func aadFor(id string, revision int, devicePublic []byte) (aadFields, error) {
	kid, err := deviceKID(devicePublic)
	if err != nil {
		return aadFields{}, err
	}
	manifest := sha256.Sum256([]byte("Beamfall ADR-0208 " + id + " manifest"))
	return aadFields{
		issuer:         "beamfall:core",
		packID:         "pack-" + id,
		profileID:      "profile-" + id,
		enrollmentID:   "enrollment-" + id,
		deviceKID:      kid,
		manifestDigest: base64.RawURLEncoding.EncodeToString(manifest[:]),
		revision:       strconv.Itoa(revision),
	}, nil
}

func populateVector(vector *Vector) error {
	transcript := vector.Transcripts[0]
	devicePublic, err := decodeHex(transcript.DevicePublicKeyHex)
	if err != nil {
		return err
	}
	devicePrivate, err := ecdhPrivate(transcript.DevicePrivateKeyHex)
	if err != nil {
		return err
	}
	ephemeralPrivate, err := ecdhPrivate(transcript.EphemeralPrivateKeyHex)
	if err != nil {
		return err
	}
	aad, err := decodeHex(transcript.AADWrapHex)
	if err != nil {
		return err
	}
	envelope, err := decodeHex(transcript.EnvelopeHex)
	if err != nil {
		return err
	}
	switch vector.ID {
	case "W01":
		vector.Fields["envelope_sha256_base64url"] = digestText(envelope)
		vector.Fields["wrapped_cek_base64url"] = base64.RawURLEncoding.EncodeToString(envelope[105:])
	case "W02":
		compressed := append([]byte{2 | (devicePublic[64] & 1)}, devicePublic[1:33]...)
		vector.Fields["compressed_device_public_key_hex"] = bytesHex(compressed)
	case "W03":
		bad := append([]byte(nil), envelope...)
		bad[40] = 4
		clear(bad[41:105])
		vector.Fields["invalid_ephemeral_public_key_hex"] = bytesHex(bad[40:105])
		vector.Fields["invalid_envelope_hex"] = bytesHex(bad)
	case "W04":
		preimage := append([]byte("BFOP-devkey-v1\x00"), devicePublic...)
		vector.Fields["label_prefix_hex"] = bytesHex([]byte("BFOP-devkey-v1\x00"))
		vector.Fields["preimage_hex"] = bytesHex(preimage)
		vector.Fields["device_kid"] = digestText(preimage)
	case "W05":
		other, err := scalarKey(201)
		if err != nil {
			return err
		}
		vector.Fields["mismatched_device_kid"], err = deviceKID(other.PublicKey().Bytes())
		return err
	case "W06":
		vector.Fields["expected_aad_wrap_hex"] = transcript.AADWrapHex
	case "W07":
		fields, err := aadFor(vector.ID, 7, devicePublic)
		if err != nil {
			return err
		}
		vector.Lists["perturbed_aad_wrap_hex"], err = perturbedAADs(fields)
		return err
	case "W08":
		vector.Fields["zero_length_field_aad_wrap_hex"] = bytesHex(zeroLengthFirstAADField(aad))
	case "W09":
		vector.Lists["wrong_length_envelopes_hex"] = []string{
			bytesHex(envelope[:len(envelope)-1]),
			bytesHex(append(append([]byte(nil), envelope...), 0)),
		}
	case "W10":
		flags := append([]byte(nil), envelope...)
		flags[6] = 1
		reserved := append([]byte(nil), envelope...)
		reserved[7] = 1
		vector.Lists["invalid_envelopes_hex"] = []string{bytesHex(flags), bytesHex(reserved)}
	case "W11":
		bad := append([]byte(nil), envelope...)
		bad[5] = 2
		vector.Fields["unknown_suite_envelope_hex"] = bytesHex(bad)
	case "W12":
		bad := append([]byte(nil), envelope...)
		bad[len(bad)-1] ^= 1
		vector.Fields["tampered_envelope_hex"] = bytesHex(bad)
	case "W13":
		vector.Fields["wrong_envelope_digest_base64url"] = digestText([]byte("wrong envelope"))
	case "W14":
		vector.Lists["reused_envelopes_hex"] = []string{
			vector.Transcripts[0].EnvelopeHex,
			vector.Transcripts[1].EnvelopeHex,
		}
	case "W15":
		manifest, plaintext, itemIndex := bfopInputs(vector.ID)
		itemDigest := sha256.Sum256(plaintext)
		vector.Fields["manifest_sha256_hex"] = bytesHex(manifest)
		vector.Fields["item_sha256_hex"] = bytesHex(itemDigest[:])
		vector.Fields["item_index"] = strconv.FormatUint(uint64(itemIndex), 10)
		vector.Fields["plaintext_hex"] = bytesHex(plaintext)
		vector.Fields["expected_header_hex"] = bytesHex(bfopHeader(manifest, itemDigest[:], itemIndex, uint64(len(plaintext))))
	case "W16", "W17", "W18", "W19", "W20":
		return populateLargeBFOPVector(vector)
	case "W21":
		other, err := scalarKey(202)
		if err != nil {
			return err
		}
		vector.Fields["other_device_private_key_hex"] = keyHex(other)
	case "W22":
		rotated, err := scalarKey(203)
		if err != nil {
			return err
		}
		vector.Fields["rotated_device_public_key_hex"] = bytesHex(rotated.PublicKey().Bytes())
		vector.Fields["rotated_device_kid"], err = deviceKID(rotated.PublicKey().Bytes())
		return err
	case "W23":
		return populateCIKVector(vector)
	case "W24":
		return populateVersionVector(vector)
	case "W25":
		return populateSizeVector(vector)
	case "W26":
		shared, err := ephemeralPrivate.ECDH(devicePrivate.PublicKey())
		if err != nil {
			return err
		}
		_, nonce, err := deriveWrap(shared, ephemeralPrivate.PublicKey().Bytes(), devicePublic, aad)
		if err != nil {
			return err
		}
		changedAAD := append([]byte(nil), aad...)
		changedAAD[len(changedAAD)-1] ^= 1
		_, changedNonce, err := deriveWrap(shared, ephemeralPrivate.PublicKey().Bytes(), devicePublic, changedAAD)
		if err != nil {
			return err
		}
		vector.Fields["nonce_hex"] = bytesHex(nonce)
		vector.Fields["repeated_nonce_hex"] = bytesHex(nonce)
		vector.Fields["changed_aad_wrap_hex"] = bytesHex(changedAAD)
		vector.Fields["changed_nonce_hex"] = bytesHex(changedNonce)
	case "W27":
		vector.Lists["attestation_cases"] = []string{
			"missing||true|true",
			"malformed|00|true|true",
			"root-unchained|3003010100|false|true",
			"exportable|3003010100|true|false",
		}
	case "W28":
		manifest, _, _ := bfopInputs(vector.ID)
		cek, _ := decodeHex(transcript.CEKHex)
		empty, err := encryptItem(cek, manifest, 0, nil)
		if err != nil {
			return err
		}
		vector.Fields["manifest_sha256_hex"] = bytesHex(manifest)
		vector.Fields["empty_item_sha256_hex"] = digestHex(empty)
		vector.Fields["empty_item_hex"] = bytesHex(empty)
		vector.Fields["encoded_size"] = strconv.Itoa(len(empty))
	}
	return nil
}

func perturbedAADs(fields aadFields) ([]string, error) {
	other := sha256.Sum256([]byte("other ADR-0208 digest"))
	otherDigest := base64.RawURLEncoding.EncodeToString(other[:])
	mutations := []func(*aadFields){
		func(f *aadFields) { f.issuer += "x" },
		func(f *aadFields) { f.packID += "x" },
		func(f *aadFields) { f.profileID += "x" },
		func(f *aadFields) { f.enrollmentID += "x" },
		func(f *aadFields) { f.deviceKID = otherDigest },
		func(f *aadFields) { f.manifestDigest = otherDigest },
		func(f *aadFields) { f.revision = "8" },
	}
	out := make([]string, 0, len(mutations))
	for _, mutate := range mutations {
		changed := fields
		mutate(&changed)
		aad, err := buildAAD(changed)
		if err != nil {
			return nil, err
		}
		out = append(out, bytesHex(aad))
	}
	return out, nil
}

func zeroLengthFirstAADField(aad []byte) []byte {
	out := append([]byte(nil), aad...)
	firstSize := 20
	firstLength := int(binary.BigEndian.Uint32(out[firstSize : firstSize+4]))
	binary.BigEndian.PutUint32(out[firstSize:firstSize+4], 0)
	return append(out[:firstSize+4], out[firstSize+4+firstLength:]...)
}

func bfopInputs(id string) ([]byte, []byte, uint32) {
	manifest := sha256.Sum256([]byte("Beamfall ADR-0208 " + id + " BFOP manifest"))
	return manifest[:], []byte("Beamfall " + id + " offline bytes"), uint32(id[2]-'0') + 7
}

func largeBFOPFixture(vector *Vector) ([]byte, []byte, []byte, uint32, []byte, error) {
	manifest, _, itemIndex := bfopInputs(vector.ID)
	cek, err := decodeHex(vector.Transcripts[0].CEKHex)
	if err != nil {
		return nil, nil, nil, 0, nil, err
	}
	plaintext := bytePattern(2*bfopChunkSize + 31)
	file, err := encryptItem(cek, manifest, itemIndex, plaintext)
	return cek, manifest, plaintext, itemIndex, file, err
}

func populateLargeBFOPVector(vector *Vector) error {
	_, manifest, plaintext, itemIndex, file, err := largeBFOPFixture(vector)
	if err != nil {
		return err
	}
	itemDigest := sha256.Sum256(plaintext)
	vector.Fields["manifest_sha256_hex"] = bytesHex(manifest)
	vector.Fields["plaintext_generator"] = "byte[i]=i mod 256 for ascending i starting at 0"
	vector.Fields["plaintext_len"] = strconv.Itoa(len(plaintext))
	vector.Fields["plaintext_sha256_hex"] = bytesHex(itemDigest[:])
	vector.Fields["item_index"] = strconv.FormatUint(uint64(itemIndex), 10)
	vector.Fields["bfop_chunk_size"] = strconv.Itoa(bfopChunkSize)
	vector.Fields["bfop_cek_source"] = "wrap_transcripts[0].cek_hex"
	vector.Fields["bfop_cik_kdf"] = "HKDF-SHA256(salt=manifest_sha256,ikm=cek,info=utf8:BFOP-cik-v1,length=32)"
	vector.Fields["bfop_header_layout"] = "utf8:BFOP||02||01||00||00||item_index:u32be||chunk_size:u32be||plaintext_len:u64be||sha256(plaintext)[0:8]||manifest_sha256"
	vector.Fields["bfop_chunk_sealing"] = "ascending chunk_index; AES-256-GCM(key=CIK,nonce=item_index:u32be||chunk_index:u64be,aad=header||chunk_index:u64be||final:u8); append ciphertext||tag"
	vector.Fields["base_header_hex"] = bytesHex(file[:bfopHeaderSize])
	vector.Fields["base_file_sha256_hex"] = digestHex(file)

	bad := append([]byte(nil), file...)
	switch vector.ID {
	case "W16":
		vector.Fields["mutation"] = "truncate-final-byte"
		bad = bad[:len(bad)-1]
	case "W17":
		vector.Fields["mutation"] = "swap-sealed-chunks-0-and-1"
		firstOffset, firstLength := chunkRange(uint64(len(plaintext)), 0)
		secondOffset, secondLength := chunkRange(uint64(len(plaintext)), 1)
		first := append([]byte(nil), bad[firstOffset:firstOffset+firstLength]...)
		copy(bad[firstOffset:firstOffset+firstLength], bad[secondOffset:secondOffset+secondLength])
		copy(bad[secondOffset:secondOffset+secondLength], first)
	case "W18":
		vector.Fields["mutation"] = "replace-sealed-chunk-0-from-splice_source_item_index"
		vector.Fields["splice_source_item_index"] = strconv.FormatUint(uint64(itemIndex+1), 10)
		vector.Fields["splice_source_inputs"] = "same cek,manifest_sha256,plaintext; item_index=splice_source_item_index"
		cek, manifest, _, _, _, err := largeBFOPFixture(vector)
		if err != nil {
			return err
		}
		other, err := encryptItem(cek, manifest, itemIndex+1, plaintext)
		if err != nil {
			return err
		}
		offset, length := chunkRange(uint64(len(plaintext)), 0)
		copy(bad[offset:offset+length], other[offset:offset+length])
	case "W19":
		vector.Fields["mutation"] = "file-unchanged-declared-plaintext-len-minus-1"
		vector.Fields["declared_plaintext_len"] = strconv.Itoa(len(plaintext) - 1)
	case "W20":
		vector.Fields["mutation"] = "replace-sealed-chunk-1-with-sealed-chunk-0"
		firstOffset, firstLength := chunkRange(uint64(len(plaintext)), 0)
		secondOffset, secondLength := chunkRange(uint64(len(plaintext)), 1)
		copy(bad[secondOffset:secondOffset+secondLength], bad[firstOffset:firstOffset+firstLength])
	}
	vector.Fields["mutated_file_sha256_hex"] = digestHex(bad)
	return nil
}

func populateCIKVector(vector *Vector) error {
	cek, err := decodeHex(vector.Transcripts[0].CEKHex)
	if err != nil {
		return err
	}
	manifestA := sha256.Sum256([]byte("Beamfall W23 revision A"))
	manifestB := sha256.Sum256([]byte("Beamfall W23 revision B"))
	cikA, err := deriveCIK(cek, manifestA[:])
	if err != nil {
		return err
	}
	cikB, err := deriveCIK(cek, manifestB[:])
	if err != nil {
		return err
	}
	plaintext := []byte("same plaintext")
	itemDigest := sha256.Sum256(plaintext)
	header := bfopHeader(manifestA[:], itemDigest[:], 9, uint64(len(plaintext)))
	nonce, aad := chunkInputs(header, 9, 0, true)
	gcm, err := newGCM(cikA)
	if err != nil {
		return err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	vector.Fields["manifest_a_sha256_hex"] = bytesHex(manifestA[:])
	vector.Fields["manifest_b_sha256_hex"] = bytesHex(manifestB[:])
	vector.Fields["cik_a_hex"] = bytesHex(cikA)
	vector.Fields["cik_b_hex"] = bytesHex(cikB)
	vector.Fields["plaintext_hex"] = bytesHex(plaintext)
	vector.Fields["item_sha256_hex"] = bytesHex(itemDigest[:])
	vector.Fields["constant_header_hex"] = bytesHex(header)
	vector.Fields["constant_aad_hex"] = bytesHex(aad)
	vector.Fields["constant_nonce_hex"] = bytesHex(nonce)
	vector.Fields["revision_a_sealed_hex"] = bytesHex(sealed)
	return nil
}

func populateVersionVector(vector *Vector) error {
	manifest, plaintext, itemIndex := bfopInputs(vector.ID)
	cek, err := decodeHex(vector.Transcripts[0].CEKHex)
	if err != nil {
		return err
	}
	file, err := encryptItem(cek, manifest, itemIndex, plaintext)
	if err != nil {
		return err
	}
	versions := make([]string, 0, 2)
	for _, version := range []byte{0, 1} {
		bad := append([]byte(nil), file...)
		bad[4] = version
		versions = append(versions, bytesHex(bad))
	}
	vector.Fields["manifest_sha256_hex"] = bytesHex(manifest)
	vector.Fields["plaintext_hex"] = bytesHex(plaintext)
	vector.Fields["item_index"] = strconv.FormatUint(uint64(itemIndex), 10)
	vector.Lists["invalid_version_files_hex"] = versions
	return nil
}

func populateSizeVector(vector *Vector) error {
	vector.Lists["plaintext_len_to_encoded_size"] = []string{
		"0:80",
		"1:81",
		"1048576:1048656",
		"1048577:1048673",
	}
	offset, length := chunkRange(bfopChunkSize+1, 1)
	vector.Fields["second_chunk_offset"] = strconv.FormatUint(offset, 10)
	vector.Fields["second_chunk_length"] = strconv.FormatUint(length, 10)
	manifest, _, _ := bfopInputs(vector.ID)
	cek, err := decodeHex(vector.Transcripts[0].CEKHex)
	if err != nil {
		return err
	}
	exact, err := encryptItem(cek, manifest, 0, bytePattern(bfopChunkSize))
	if err != nil {
		return err
	}
	vector.Fields["exact_multiple_file_sha256_hex"] = digestHex(exact)
	return nil
}

func ecdhPrivate(encoded string) (*ecdh.PrivateKey, error) {
	raw, err := decodeHex(encoded)
	if err != nil {
		return nil, err
	}
	return ecdh.P256().NewPrivateKey(raw)
}
