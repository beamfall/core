package wrapvectors

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func Verify(encoded []byte) error {
	corpus, err := Decode(encoded)
	if err != nil {
		return err
	}
	if err := ValidateCoverage(corpus); err != nil {
		return err
	}
	if err := ValidateTupleRule(corpus); err != nil {
		return err
	}
	for index := range corpus.Vectors {
		vector := &corpus.Vectors[index]
		for transcriptIndex, transcript := range vector.Transcripts {
			if err := verifyTranscript(vector.ID, index+1, transcriptIndex, transcript); err != nil {
				return fmt.Errorf("%s transcript: %w", vector.ID, err)
			}
		}
		if err := verifyVector(vector); err != nil {
			return fmt.Errorf("%s reference verification: %w", vector.ID, err)
		}
	}
	return nil
}

// ValidateBFOPReconstruction rebuilds W16-W20 solely from their published
// fields and checks the byte pins.
func ValidateBFOPReconstruction(corpus Corpus) error {
	for index := range corpus.Vectors {
		vector := &corpus.Vectors[index]
		if vector.ID < "W16" || vector.ID > "W20" {
			continue
		}
		if err := verifyLargeBFOPVector(vector); err != nil {
			return fmt.Errorf("%s reconstruction: %w", vector.ID, err)
		}
	}
	return nil
}

func verifyTranscript(id string, index, transcriptIndex int, transcript WrapTranscript) error {
	device, err := ecdhPrivate(transcript.DevicePrivateKeyHex)
	if err != nil {
		return err
	}
	ephemeral, err := ecdhPrivate(transcript.EphemeralPrivateKeyHex)
	if err != nil {
		return err
	}
	if bytesHex(device.PublicKey().Bytes()) != transcript.DevicePublicKeyHex {
		return fmt.Errorf("device public key does not match private key")
	}
	if bytesHex(ephemeral.PublicKey().Bytes()) != transcript.EphemeralPublicKeyHex {
		return fmt.Errorf("ephemeral public key does not match private key")
	}
	fields, err := aadFor(id, index, device.PublicKey().Bytes())
	if err != nil {
		return err
	}
	aad, err := buildAAD(fields)
	if err != nil {
		return err
	}
	if bytesHex(aad) != transcript.AADWrapHex {
		return fmt.Errorf("AAD_wrap bytes drifted")
	}
	cek := sha256.Sum256([]byte("Beamfall ADR-0208 " + id + " deterministic CEK"))
	if id == "W14" && transcriptIndex == 1 {
		cek = sha256.Sum256([]byte("Beamfall ADR-0208 W14 deterministic reused-transcript CEK"))
	}
	if bytesHex(cek[:]) != transcript.CEKHex {
		return fmt.Errorf("CEK bytes drifted")
	}
	envelope, err := wrap(device.PublicKey().Bytes(), aad, cek[:], ephemeral)
	if err != nil {
		return err
	}
	if bytesHex(envelope) != transcript.EnvelopeHex {
		return fmt.Errorf("BFKW bytes drifted")
	}
	return nil
}

func verifyVector(vector *Vector) error {
	transcript := vector.Transcripts[0]
	device, err := ecdhPrivate(transcript.DevicePrivateKeyHex)
	if err != nil {
		return err
	}
	ephemeral, err := ecdhPrivate(transcript.EphemeralPrivateKeyHex)
	if err != nil {
		return err
	}
	devicePublic, _ := decodeHex(transcript.DevicePublicKeyHex)
	aad, _ := decodeHex(transcript.AADWrapHex)
	cek, _ := decodeHex(transcript.CEKHex)
	envelope, _ := decodeHex(transcript.EnvelopeHex)
	kid, err := deviceKID(devicePublic)
	if err != nil {
		return err
	}

	switch vector.ID {
	case "W01":
		if field(vector, "envelope_sha256_base64url") != digestText(envelope) {
			return fmt.Errorf("envelope digest drifted")
		}
		if field(vector, "wrapped_cek_base64url") != base64.RawURLEncoding.EncodeToString(envelope[105:]) {
			return fmt.Errorf("wrapped CEK bytes drifted")
		}
		got, err := unwrap(device, envelope, aad, kid, digestText(envelope))
		if err != nil || !bytes.Equal(got, cek) {
			return fmt.Errorf("golden unwrap = %x, %v", got, err)
		}
	case "W02":
		compressed := fieldHex(vector, "compressed_device_public_key_hex")
		if _, err := parsePublic(compressed); err == nil {
			return fmt.Errorf("compressed device point accepted")
		}
	case "W03":
		bad := fieldHex(vector, "invalid_envelope_hex")
		if _, err := unwrap(device, bad, aad, kid, digestText(bad)); err == nil {
			return fmt.Errorf("invalid ephemeral point accepted")
		}
	case "W04":
		preimage := append([]byte("BFOP-devkey-v1\x00"), devicePublic...)
		if field(vector, "label_prefix_hex") != bytesHex([]byte("BFOP-devkey-v1\x00")) ||
			field(vector, "preimage_hex") != bytesHex(preimage) ||
			field(vector, "device_kid") != digestText(preimage) {
			return fmt.Errorf("device_kid preimage or digest drifted")
		}
	case "W05":
		if _, err := unwrap(device, envelope, aad, field(vector, "mismatched_device_kid"), digestText(envelope)); err == nil {
			return fmt.Errorf("mismatched device_kid accepted")
		}
	case "W06":
		if field(vector, "expected_aad_wrap_hex") != transcript.AADWrapHex {
			return fmt.Errorf("golden AAD_wrap drifted")
		}
	case "W07":
		values := list(vector, "perturbed_aad_wrap_hex")
		if len(values) != 7 {
			return fmt.Errorf("perturbed AAD count = %d", len(values))
		}
		for index, value := range values {
			changed, err := decodeHex(value)
			if err != nil {
				return err
			}
			if _, err := unwrap(device, envelope, changed, kid, digestText(envelope)); err == nil {
				return fmt.Errorf("perturbed AAD field %d accepted", index)
			}
		}
	case "W08":
		if _, err := parseAAD(fieldHex(vector, "zero_length_field_aad_wrap_hex")); err == nil {
			return fmt.Errorf("zero-length AAD field accepted")
		}
	case "W09":
		for _, value := range list(vector, "wrong_length_envelopes_hex") {
			if err := validateEnvelope(fieldHexValue(value)); err == nil {
				return fmt.Errorf("wrong-length envelope accepted")
			}
		}
	case "W10":
		for _, value := range list(vector, "invalid_envelopes_hex") {
			if err := validateEnvelope(fieldHexValue(value)); err == nil {
				return fmt.Errorf("non-zero flags or reserved accepted")
			}
		}
	case "W11":
		if err := validateEnvelope(fieldHex(vector, "unknown_suite_envelope_hex")); err == nil {
			return fmt.Errorf("unknown suite accepted")
		}
	case "W12":
		bad := fieldHex(vector, "tampered_envelope_hex")
		if _, err := unwrap(device, bad, aad, kid, digestText(bad)); err == nil {
			return fmt.Errorf("tampered GCM tag accepted")
		}
	case "W13":
		if _, err := unwrap(device, envelope, aad, kid, field(vector, "wrong_envelope_digest_base64url")); err == nil {
			return fmt.Errorf("wrong envelope digest accepted")
		}
	case "W14":
		reused := list(vector, "reused_envelopes_hex")
		if len(reused) != 2 || reused[0] == reused[1] {
			return fmt.Errorf("reused transcript does not pin two distinct wraps")
		}
		for index, transcript := range vector.Transcripts {
			if reused[index] != transcript.EnvelopeHex {
				return fmt.Errorf("reused envelope %d drifted", index)
			}
		}
		if err := ValidateProductionTupleRule(vector.Transcripts); err == nil {
			return fmt.Errorf("production accepted repeated wrap tuple")
		}
	case "W15":
		manifest := fieldHex(vector, "manifest_sha256_hex")
		plaintext := fieldHex(vector, "plaintext_hex")
		itemDigest := sha256.Sum256(plaintext)
		itemIndex, err := fieldUint(vector, "item_index")
		if err != nil {
			return err
		}
		header := bfopHeader(manifest, itemDigest[:], itemIndex, uint64(len(plaintext)))
		if field(vector, "expected_header_hex") != bytesHex(header) {
			return fmt.Errorf("BFOP header drifted")
		}
	case "W16", "W17", "W18", "W19", "W20":
		if err := verifyLargeBFOPVector(vector); err != nil {
			return err
		}
	case "W21":
		other, err := ecdhPrivate(field(vector, "other_device_private_key_hex"))
		if err != nil {
			return err
		}
		if _, err := unwrap(other, envelope, aad, kid, digestText(envelope)); err == nil {
			return fmt.Errorf("cross-device unwrap accepted")
		}
	case "W22":
		if _, err := unwrap(device, envelope, aad, field(vector, "rotated_device_kid"), digestText(envelope)); err == nil {
			return fmt.Errorf("old envelope accepted after rotation")
		}
	case "W23":
		if err := verifyCIKVector(vector, cek); err != nil {
			return err
		}
	case "W24":
		if err := verifyVersionVector(vector, cek); err != nil {
			return err
		}
	case "W25":
		if err := verifySizeVector(vector, cek); err != nil {
			return err
		}
	case "W26":
		shared, err := ephemeral.ECDH(device.PublicKey())
		if err != nil {
			return err
		}
		_, nonce, err := deriveWrap(shared, ephemeral.PublicKey().Bytes(), devicePublic, aad)
		if err != nil {
			return err
		}
		changedAAD := fieldHex(vector, "changed_aad_wrap_hex")
		_, changedNonce, err := deriveWrap(shared, ephemeral.PublicKey().Bytes(), devicePublic, changedAAD)
		if err != nil {
			return err
		}
		if field(vector, "nonce_hex") != bytesHex(nonce) ||
			field(vector, "repeated_nonce_hex") != bytesHex(nonce) ||
			field(vector, "changed_nonce_hex") != bytesHex(changedNonce) ||
			bytes.Equal(nonce, changedNonce) {
			return fmt.Errorf("transcript-derived nonce relation drifted")
		}
	case "W27":
		if err := verifyAttestationVector(vector); err != nil {
			return err
		}
	case "W28":
		manifest := fieldHex(vector, "manifest_sha256_hex")
		file := fieldHex(vector, "empty_item_hex")
		if len(file) != 80 || field(vector, "encoded_size") != "80" ||
			field(vector, "empty_item_sha256_hex") != digestHex(file) {
			return fmt.Errorf("empty item bytes or size drifted")
		}
		emptyDigest := sha256.Sum256(nil)
		got, err := decryptItem(cek, manifest, 0, 0, emptyDigest[:], file)
		if err != nil || len(got) != 0 {
			return fmt.Errorf("empty item decrypt = %x, %v", got, err)
		}
	}
	return nil
}

func verifyLargeBFOPVector(vector *Vector) error {
	if len(vector.Transcripts) == 0 {
		return fmt.Errorf("wrap transcript is missing")
	}
	cek := fieldHexValue(vector.Transcripts[0].CEKHex)
	manifest := fieldHex(vector, "manifest_sha256_hex")
	plaintextLen, err := fieldUint64(vector, "plaintext_len")
	if err != nil {
		return err
	}
	itemIndex, err := fieldUint(vector, "item_index")
	if err != nil {
		return err
	}
	if plaintextLen > math.MaxInt {
		return fmt.Errorf("plaintext_len is not addressable")
	}
	if field(vector, "plaintext_generator") != "byte[i]=i mod 256 for ascending i starting at 0" ||
		field(vector, "bfop_chunk_size") != strconv.Itoa(bfopChunkSize) ||
		field(vector, "bfop_cek_source") != "wrap_transcripts[0].cek_hex" ||
		field(vector, "bfop_cik_kdf") != "HKDF-SHA256(salt=manifest_sha256,ikm=cek,info=utf8:BFOP-cik-v1,length=32)" ||
		field(vector, "bfop_header_layout") != "utf8:BFOP||02||01||00||00||item_index:u32be||chunk_size:u32be||plaintext_len:u64be||sha256(plaintext)[0:8]||manifest_sha256" ||
		field(vector, "bfop_chunk_sealing") != "ascending chunk_index; AES-256-GCM(key=CIK,nonce=item_index:u32be||chunk_index:u64be,aad=header||chunk_index:u64be||final:u8); append ciphertext||tag" {
		return fmt.Errorf("large BFOP reconstruction recipe is incomplete")
	}
	plaintext := bytePattern(int(plaintextLen))
	itemDigest := sha256.Sum256(plaintext)
	if field(vector, "plaintext_sha256_hex") != bytesHex(itemDigest[:]) {
		return fmt.Errorf("large BFOP plaintext digest drifted")
	}
	file, err := encryptItem(cek, manifest, itemIndex, plaintext)
	if err != nil {
		return err
	}
	if field(vector, "base_header_hex") != bytesHex(file[:bfopHeaderSize]) ||
		field(vector, "base_file_sha256_hex") != digestHex(file) {
		return fmt.Errorf("large BFOP base fixture drifted")
	}
	bad := append([]byte(nil), file...)
	declaredLength := uint64(len(plaintext))
	switch field(vector, "mutation") {
	case "truncate-final-byte":
		bad = bad[:len(bad)-1]
	case "swap-sealed-chunks-0-and-1":
		firstOffset, firstLength := chunkRange(uint64(len(plaintext)), 0)
		secondOffset, secondLength := chunkRange(uint64(len(plaintext)), 1)
		first := append([]byte(nil), bad[firstOffset:firstOffset+firstLength]...)
		copy(bad[firstOffset:firstOffset+firstLength], bad[secondOffset:secondOffset+secondLength])
		copy(bad[secondOffset:secondOffset+secondLength], first)
	case "replace-sealed-chunk-0-from-splice_source_item_index":
		if field(vector, "splice_source_inputs") != "same cek,manifest_sha256,plaintext; item_index=splice_source_item_index" {
			return fmt.Errorf("splice source reconstruction recipe is incomplete")
		}
		otherIndex, err := fieldUint(vector, "splice_source_item_index")
		if err != nil {
			return err
		}
		other, err := encryptItem(cek, manifest, otherIndex, plaintext)
		if err != nil {
			return err
		}
		offset, length := chunkRange(uint64(len(plaintext)), 0)
		copy(bad[offset:offset+length], other[offset:offset+length])
	case "file-unchanged-declared-plaintext-len-minus-1":
		declaredLength, err = fieldUint64(vector, "declared_plaintext_len")
		if err != nil {
			return err
		}
		if field(vector, "declared_plaintext_len") != strconv.FormatUint(declaredLength, 10) {
			return fmt.Errorf("declared plaintext length drifted")
		}
		if declaredLength+1 != uint64(len(plaintext)) {
			return fmt.Errorf("declared plaintext length is not actual length minus one")
		}
	case "replace-sealed-chunk-1-with-sealed-chunk-0":
		firstOffset, firstLength := chunkRange(uint64(len(plaintext)), 0)
		secondOffset, secondLength := chunkRange(uint64(len(plaintext)), 1)
		copy(bad[secondOffset:secondOffset+secondLength], bad[firstOffset:firstOffset+firstLength])
	default:
		return fmt.Errorf("unknown large BFOP mutation recipe")
	}
	if field(vector, "mutated_file_sha256_hex") != digestHex(bad) {
		return fmt.Errorf("mutated BFOP digest drifted")
	}
	if _, err := decryptItem(cek, manifest, itemIndex, declaredLength, itemDigest[:], bad); err == nil {
		return fmt.Errorf("invalid BFOP file accepted")
	}
	return nil
}

func verifyCIKVector(vector *Vector, cek []byte) error {
	manifestA := fieldHex(vector, "manifest_a_sha256_hex")
	manifestB := fieldHex(vector, "manifest_b_sha256_hex")
	cikA, err := deriveCIK(cek, manifestA)
	if err != nil {
		return err
	}
	cikB, err := deriveCIK(cek, manifestB)
	if err != nil {
		return err
	}
	if bytes.Equal(cikA, cikB) ||
		field(vector, "cik_a_hex") != bytesHex(cikA) ||
		field(vector, "cik_b_hex") != bytesHex(cikB) {
		return fmt.Errorf("CIK separation drifted")
	}
	plaintext := fieldHex(vector, "plaintext_hex")
	itemDigest := sha256.Sum256(plaintext)
	header := fieldHex(vector, "constant_header_hex")
	wantHeader := bfopHeader(manifestA, itemDigest[:], 9, uint64(len(plaintext)))
	if !bytes.Equal(header, wantHeader) ||
		field(vector, "item_sha256_hex") != bytesHex(itemDigest[:]) {
		return fmt.Errorf("constant valid BFOP header drifted")
	}
	nonce := fieldHex(vector, "constant_nonce_hex")
	aad := fieldHex(vector, "constant_aad_hex")
	sealed := fieldHex(vector, "revision_a_sealed_hex")
	wantNonce, wantAAD := chunkInputs(header, 9, 0, true)
	if !bytes.Equal(nonce, wantNonce) || !bytes.Equal(aad, wantAAD) {
		return fmt.Errorf("constant chunk inputs drifted")
	}
	gcmB, err := newGCM(cikB)
	if err != nil {
		return err
	}
	if _, err := gcmB.Open(nil, nonce, sealed, aad); err == nil {
		return fmt.Errorf("revision B CIK authenticated revision A ciphertext")
	}
	return nil
}

func verifyVersionVector(vector *Vector, cek []byte) error {
	manifest := fieldHex(vector, "manifest_sha256_hex")
	plaintext := fieldHex(vector, "plaintext_hex")
	itemDigest := sha256.Sum256(plaintext)
	itemIndex, err := fieldUint(vector, "item_index")
	if err != nil {
		return err
	}
	for _, value := range list(vector, "invalid_version_files_hex") {
		file := fieldHexValue(value)
		if len(file) < 5 || (file[4] != 0 && file[4] != 1) {
			return fmt.Errorf("invalid-version fixture does not carry v0/v1")
		}
		if _, err := decryptItem(cek, manifest, itemIndex, uint64(len(plaintext)), itemDigest[:], file); err == nil {
			return fmt.Errorf("BFOP version %d accepted", file[4])
		}
	}
	return nil
}

func verifySizeVector(vector *Vector, cek []byte) error {
	for _, pair := range list(vector, "plaintext_len_to_encoded_size") {
		parts := strings.Split(pair, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid size table entry %q", pair)
		}
		plaintextLen, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return err
		}
		want, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return err
		}
		if bfopSize(plaintextLen) != want {
			return fmt.Errorf("BFOP size(%d) = %d, want %d", plaintextLen, bfopSize(plaintextLen), want)
		}
	}
	offset, length := chunkRange(bfopChunkSize+1, 1)
	if field(vector, "second_chunk_offset") != strconv.FormatUint(offset, 10) ||
		field(vector, "second_chunk_length") != strconv.FormatUint(length, 10) {
		return fmt.Errorf("BFOP chunk range drifted")
	}
	manifest, _, _ := bfopInputs(vector.ID)
	exact, err := encryptItem(cek, manifest, 0, bytePattern(bfopChunkSize))
	if err != nil {
		return err
	}
	if field(vector, "exact_multiple_file_sha256_hex") != digestHex(exact) {
		return fmt.Errorf("exact-multiple file drifted")
	}
	return nil
}

func verifyAttestationVector(vector *Vector) error {
	cases := list(vector, "attestation_cases")
	if len(cases) != 4 {
		return fmt.Errorf("attestation case count = %d", len(cases))
	}
	for _, encoded := range cases {
		parts := strings.Split(encoded, "|")
		if len(parts) != 4 {
			return fmt.Errorf("invalid attestation case %q", encoded)
		}
		attestation := fieldHexValue(parts[1])
		rootChained, err := strconv.ParseBool(parts[2])
		if err != nil {
			return err
		}
		nonExportable, err := strconv.ParseBool(parts[3])
		if err != nil {
			return err
		}
		if referenceAttestationAccepts(attestation, rootChained, nonExportable) {
			return fmt.Errorf("%s attestation accepted", parts[0])
		}
	}
	return nil
}

func referenceAttestationAccepts(attestation []byte, rootChained, nonExportable bool) bool {
	wellFormed := len(attestation) >= 5 && attestation[0] == 0x30
	return wellFormed && rootChained && nonExportable
}

func validateEnvelope(envelope []byte) error {
	if len(envelope) != bfkwSize || !bytes.Equal(envelope[:8], bfkwPrefix[:]) {
		return fmt.Errorf("invalid BFKW envelope")
	}
	_, err := parsePublic(envelope[40:105])
	return err
}

func field(vector *Vector, name string) string {
	return vector.Fields[name]
}

func list(vector *Vector, name string) []string {
	return vector.Lists[name]
}

func fieldHex(vector *Vector, name string) []byte {
	return fieldHexValue(field(vector, name))
}

func fieldHexValue(value string) []byte {
	decoded, _ := decodeHex(value)
	return decoded
}

func fieldUint(vector *Vector, name string) (uint32, error) {
	value, err := strconv.ParseUint(field(vector, name), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if value > math.MaxUint32 {
		return 0, fmt.Errorf("%s exceeds uint32", name)
	}
	return uint32(value), nil
}

func fieldUint64(vector *Vector, name string) (uint64, error) {
	value, err := strconv.ParseUint(field(vector, name), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return value, nil
}
