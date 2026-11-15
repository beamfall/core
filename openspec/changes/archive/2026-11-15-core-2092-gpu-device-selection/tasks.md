## 1. Enumeration and selection

- [x] 1.1 `ffmpeg.EnumerateRenderNodes` reads render nodes and PCI vendor/device IDs from injectable roots
- [x] 1.2 `ffmpeg.SelectRenderNode` honours a node path or vendor name, else Intel, AMD, first
- [x] 1.3 Tests: Intel + AMD two-node fixture, each selection rule, an unmatched hint

## 2. Device use

- [x] 2.1 `ffmpeg.SetRenderNode` makes the selected node the device for probe, tone-map and encode
- [x] 2.2 QSV probe and encode initialise QSV on the selected node; tests pin the argv

## 3. Report

- [x] 3.1 The internal hardware-acceleration report and startup log carry the nodes and the selection
