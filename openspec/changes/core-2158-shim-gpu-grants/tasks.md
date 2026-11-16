## 1. Shim

- [x] 1.1 `--dri` shim flag, parsed and threaded to the Linux ruleset
- [x] 1.2 Ruleset: `/dev/dri` listing, `renderD*` read/write/ioctl, `/etc/OpenCL/vendors` read-only, each only when present
- [x] 1.3 Tests: ruleset contents with and without `--dri`, ioctl only at ABI 5+, no `card*` node

## 2. Transcodes

- [x] 2.1 HLS jobs request `--dri` only for a QSV or VA-API encode or tone-map
- [x] 2.2 Tests: shim argv carries `--dri` for a hardware rendition and not for software

## 3. Probes

- [x] 3.1 Encoder, tone-map and device-decode probes run through the configured shim with the transcode's grants
- [x] 3.2 Device-decode sample written to a private directory granted read-only
- [x] 3.3 `beamfall serve` configures the probe shim
- [x] 3.4 Tests: probe commands wrapped with `--dri` and the sample directory; direct exec without a shim

## 4. Hardware receipt

- [ ] 4.1 QSV or VA-API transcode with an OpenCL tone-map through the shim on a landlock ABI 3+ Intel host
