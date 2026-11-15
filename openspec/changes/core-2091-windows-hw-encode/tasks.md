## 1. Probe

- [x] 1.1 `amf` accelerator with `h264_amf`, `hevc_amf`, `av1_amf`
- [x] 1.2 Windows candidates NVENC, QSV, AMF; `amf` report row only on Windows
- [x] 1.3 Windows QSV device init on D3D11

## 2. Encode

- [x] 2.1 AMF NV12 ingest and capped-VBR rate control
- [x] 2.2 Tests: Windows candidates, report rows per platform, QSV Windows init, AMF argv

## 3. Hardware receipt

- [ ] 3.1 Windows probe on real NVIDIA, Intel and AMD hardware — needs CORE-1042
