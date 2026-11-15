## 1. AV1

- [x] 1.1 AV1 encoder table (`av1_qsv`, `av1_vaapi`, `av1_nvenc`) probed behind a passing H.264 probe
- [x] 1.2 Unverified AV1 renditions take the software path; `SupportsTranscodeCodec("av1")` follows the probe
- [ ] 1.3 AV1 delivery (grant field, ladder rungs, `av01` CODECS) — wire work, CORE-2100

## 2. Low power

- [x] 2.1 QSV/VA-API H.264 probe retried with `-low_power 1` when the normal probe fails
- [x] 2.2 Low-power accelerators pass `-low_power 1` on follow-up probes and real encodes
- [x] 2.3 Tests: normal pass, normal fail + low-power pass, both fail, probe and encode argv

## 3. Report

- [x] 3.1 The internal hardware-acceleration report and startup log carry AV1 and low-power mode
