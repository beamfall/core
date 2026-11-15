# GPU device selection on multi-GPU hosts

Ticket: CORE-2092 (`queue:core:main`).

## Why

The QSV/VA-API probe, the hardware tone-map and the encode each used the first sorted
`/dev/dri/renderD*` node, and the QSV encoder initialised no device at all, so libmfx picked its
own adapter. On a host with an Intel iGPU and an AMD dGPU the probe could verify one GPU while the
encode ran on another, and the stored transcode device (documented as "a /dev/dri render node")
was never honoured.

## What Changes

- At startup the server enumerates `/dev/dri/renderD*` and reads each node's PCI vendor and device
  ID from `/sys/class/drm/<node>/device/{vendor,device}` (0x8086 Intel, 0x1002 AMD, 0x10de NVIDIA).
- It selects one render node:
  - a stored transcode device that is an enumerated render node path selects that node;
  - a stored device of `intel`, `amd` or `nvidia` selects the first node of that vendor;
  - otherwise the first Intel node, then the first AMD node, then the first node.
  - A stored hint that matches no node is logged and falls back to the default order.
- The selected node is used for the QSV and VA-API probe encodes, the VA-API/QSV tone-map device
  and the encode, so probe and encode always run on the same GPU.
- The QSV probe and encode initialise QSV explicitly on the selected node
  (`-init_hw_device qsv=bfqsv:hw,child_device=<node>`), unless the QSV tone-map chain already
  initialised a QSV device on that node, which the encoder then uses.
- The QSV tone-map device moves to the same `child_device` form. A QSV device string names the MFX
  implementation, so the old `qsv=hw:<node>` never selected a node.
- The node list (path, vendor, vendor/device IDs) and the selection are recorded in the internal
  hardware-acceleration report and the startup log only. Nothing is added to the wire.
- NVIDIA device selection (`-gpu N`) is out of scope: CUDA device order does not map cheaply to DRM
  render nodes.

## Impact

- `internal/ffmpeg` (enumeration, selection, device arguments), `internal/config` (report fields),
  `internal/app` (startup wiring and log).
- No wire change. Console display of the node list is CORE-2100.
