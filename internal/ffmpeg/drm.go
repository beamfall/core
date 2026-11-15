package ffmpeg

// DRM render-node selection (CORE-2092). QSV and VA-API both run on a
// /dev/dri render node. On a multi-GPU host (an Intel iGPU beside an AMD
// dGPU, say) the probe, the hardware tone-map and the encode must all use the
// same node, or the probe verifies one GPU while the encode runs on another.
// The server enumerates the nodes once at startup, selects one, and installs
// it with SetRenderNode; vaapiDevicePath then returns it everywhere.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
)

// Default enumeration roots. EnumerateRenderNodes takes them as parameters so
// tests can point it at a fixture tree.
const (
	DefaultDRIDir = "/dev/dri"
	DefaultDRMDir = "/sys/class/drm"
)

// PCI vendor IDs as sysfs prints them, and the vendor names a stored transcode
// device may use to pick a node.
var pciVendorNames = map[string]string{
	"0x8086": "intel",
	"0x1002": "amd",
	"0x10de": "nvidia",
}

// RenderNode is one DRM render node and the PCI identity of the GPU behind it.
// Vendor is "intel", "amd", "nvidia" or "" when sysfs names another or none.
type RenderNode struct {
	Path     string
	Vendor   string
	VendorID string
	DeviceID string
}

// EnumerateRenderNodes lists the renderD* nodes under driDir in name order,
// reading each node's PCI vendor and device ID from
// <drmDir>/<node>/device/{vendor,device}. A node whose sysfs entry is missing
// is still listed, with empty IDs. A missing driDir yields nil.
func EnumerateRenderNodes(driDir, drmDir string) []RenderNode {
	entries, err := os.ReadDir(driDir)
	if err != nil {
		return nil
	}
	var nodes []RenderNode
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "renderD") {
			continue
		}
		device := filepath.Join(drmDir, name, "device")
		vendorID := readSysfsID(filepath.Join(device, "vendor"))
		nodes = append(nodes, RenderNode{
			Path:     filepath.Join(driDir, name),
			Vendor:   pciVendorNames[vendorID],
			VendorID: vendorID,
			DeviceID: readSysfsID(filepath.Join(device, "device")),
		})
	}
	slices.SortFunc(nodes, func(a, b RenderNode) int { return strings.Compare(a.Path, b.Path) })
	return nodes
}

func readSysfsID(path string) string {
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed sysfs layout under a server-chosen root
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(raw)))
}

// renderNodeVendorOrder is the default preference when no hint matches: Intel
// first (Quick Sync and VA-API on the NAS-majority iGPU), then AMD.
var renderNodeVendorOrder = []string{"intel", "amd"}

// SelectRenderNode picks the render node hardware transcode uses. hint is the
// stored transcode device: an enumerated render node path selects that node;
// "intel", "amd" or "nvidia" (any case) selects the first node of that vendor.
// Otherwise, or when the hint matches no node (hintMatched false), it selects
// the first Intel node, then the first AMD node, then the first node. ok is
// false only when there are no nodes.
func SelectRenderNode(nodes []RenderNode, hint string) (node RenderNode, hintMatched, ok bool) {
	if len(nodes) == 0 {
		return RenderNode{}, false, false
	}
	hint = strings.TrimSpace(hint)
	if strings.HasPrefix(hint, "/") {
		clean := filepath.Clean(hint)
		if i := slices.IndexFunc(nodes, func(n RenderNode) bool { return n.Path == clean }); i >= 0 {
			return nodes[i], true, true
		}
	} else if vendor := strings.ToLower(hint); slices.Contains(renderNodeVendorNames(), vendor) {
		if i := slices.IndexFunc(nodes, func(n RenderNode) bool { return n.Vendor == vendor }); i >= 0 {
			return nodes[i], true, true
		}
	}
	for _, vendor := range renderNodeVendorOrder {
		if i := slices.IndexFunc(nodes, func(n RenderNode) bool { return n.Vendor == vendor }); i >= 0 {
			return nodes[i], false, true
		}
	}
	return nodes[0], false, true
}

func renderNodeVendorNames() []string {
	names := make([]string, 0, len(pciVendorNames))
	for _, name := range pciVendorNames {
		names = append(names, name)
	}
	return names
}

// RenderNodeHint reports whether a stored transcode device is meant as a
// render-node hint (a path or a vendor name) rather than an accelerator name,
// so the caller can log a hint that matched no node.
func RenderNodeHint(device string) bool {
	device = strings.TrimSpace(device)
	return strings.HasPrefix(device, "/") || slices.Contains(renderNodeVendorNames(), strings.ToLower(device))
}

// selectedRenderNode is the process-wide render node installed at startup.
var selectedRenderNode atomic.Pointer[string]

// SetRenderNode installs the render node the QSV/VA-API probe, tone-map and
// encode use. "" restores the default (the first sorted node). Call it before
// the hardware probe so probe and encode agree.
func SetRenderNode(path string) {
	if path == "" {
		selectedRenderNode.Store(nil)
		return
	}
	selectedRenderNode.Store(&path)
}

// qsvDeviceName names the encoder's own QSV device. It differs from the
// tone-map chain's device names ("hw", "va") so the two never collide.
const qsvDeviceName = "bfqsv"

// qsvInitArgs initialises the QSV device the encoder picks up, on the selected
// render node, so the encode runs on the GPU the probe verified rather than
// whichever adapter libmfx would pick on its own.
func qsvInitArgs() []string {
	return []string{"-init_hw_device", qsvDeviceSpec(qsvDeviceName, vaapiDevicePath())}
}

// qsvDeviceSpec is an -init_hw_device value for a QSV device on node. For QSV
// the device string selects the MFX implementation, not a node ("hw" is the
// hardware implementation); the node goes in child_device, which FFmpeg opens
// as the child VA-API device the QSV session runs on.
// On Windows QSV runs on a D3D11 child device and no node applies (CORE-2091).
func qsvDeviceSpec(name, node string) string {
	if hostOS == "windows" {
		return "qsv=" + name + ":hw_any,child_device_type=d3d11va"
	}
	return "qsv=" + name + ":hw,child_device=" + node
}
