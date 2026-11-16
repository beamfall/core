## 1. Shim

- [ ] 1.1 `--nvidia` shim flag, parsed and threaded to the Linux ruleset
- [ ] 1.2 Ruleset: `nvidiactl`, `nvidia-uvm`, `nvidia[0-9]*` read/write/ioctl, `/proc/driver/nvidia` read-only, each GPU's sysfs PCI device directory read-only, each only when present
- [ ] 1.3 Tests: ruleset contents with and without `--nvidia`, ioctl only at ABI 5+, no `nvidia-modeset`, `nvidia-uvm-tools` or `nvidia-caps`; rules valid in a real ruleset; grant enforced in a confined child

## 2. Transcodes

- [ ] 2.1 HLS jobs request `--nvidia` only for an NVENC encode or CUDA tone-map
- [ ] 2.2 Tests: shim argv carries `--nvidia` for an NVENC rendition and not for software, QSV or VA-API

## 3. Probes

- [ ] 3.1 Encoder, tone-map and device-decode probes request `--nvidia` exactly when their transcode would
- [ ] 3.2 Tests: NVENC encoder probe and CUDA tone-map renders carry `--nvidia` and no `--dri`

## 4. Hardware receipt

- [ ] 4.1 NVENC transcode with a CUDA tone-map through the shim on a landlock ABI 3+ NVIDIA host
