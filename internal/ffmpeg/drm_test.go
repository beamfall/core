package ffmpeg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// renderNodeFixture builds a dev/dri + sys/class/drm tree: renderD128 is an
// AMD dGPU and renderD129 an Intel iGPU, the order that defeated the old
// first-sorted-node rule. card0 and a stray file must be ignored.
func renderNodeFixture(t *testing.T) (driDir, drmDir string) {
	t.Helper()
	root := t.TempDir()
	driDir = filepath.Join(root, "dev", "dri")
	drmDir = filepath.Join(root, "sys", "class", "drm")
	for name, ids := range map[string][2]string{
		"renderD128": {"0x1002\n", "0x744c\n"},
		"renderD129": {"0x8086\n", "0x56A0\n"},
	} {
		writeFixtureFile(t, filepath.Join(driDir, name), "")
		writeFixtureFile(t, filepath.Join(drmDir, name, "device", "vendor"), ids[0])
		writeFixtureFile(t, filepath.Join(drmDir, name, "device", "device"), ids[1])
	}
	writeFixtureFile(t, filepath.Join(driDir, "card0"), "")
	return driDir, drmDir
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnumerateRenderNodesReadsVendorAndDevice(t *testing.T) {
	driDir, drmDir := renderNodeFixture(t)
	got := EnumerateRenderNodes(driDir, drmDir)
	want := []RenderNode{
		{Path: filepath.Join(driDir, "renderD128"), Vendor: "amd", VendorID: "0x1002", DeviceID: "0x744c"},
		{Path: filepath.Join(driDir, "renderD129"), Vendor: "intel", VendorID: "0x8086", DeviceID: "0x56a0"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("EnumerateRenderNodes = %+v, want %+v", got, want)
	}
	if nodes := EnumerateRenderNodes(filepath.Join(driDir, "missing"), drmDir); nodes != nil {
		t.Fatalf("missing dri dir = %+v, want nil", nodes)
	}
}

func TestEnumerateRenderNodesToleratesMissingSysfs(t *testing.T) {
	driDir, _ := renderNodeFixture(t)
	got := EnumerateRenderNodes(driDir, filepath.Join(t.TempDir(), "nosys"))
	if len(got) != 2 || got[0].Vendor != "" || got[0].VendorID != "" {
		t.Fatalf("nodes without sysfs = %+v, want two nodes with empty IDs", got)
	}
	if node, _, _ := SelectRenderNode(got, ""); node.Path != got[0].Path {
		t.Fatalf("unknown vendors select %q, want the first node", node.Path)
	}
}

func TestSelectRenderNode(t *testing.T) {
	driDir, drmDir := renderNodeFixture(t)
	nodes := EnumerateRenderNodes(driDir, drmDir)
	amd, intel := filepath.Join(driDir, "renderD128"), filepath.Join(driDir, "renderD129")
	cases := []struct {
		name        string
		hint        string
		want        string
		hintMatched bool
	}{
		{"no hint prefers intel", "", intel, false},
		{"accelerator name is not a node hint", "vaapi", intel, false},
		{"path selects that node", amd, amd, true},
		{"path is cleaned", filepath.Join(driDir, ".", "renderD128"), amd, true},
		{"vendor name selects that vendor", "AMD", amd, true},
		{"vendor name intel", "intel", intel, true},
		{"unknown path falls back", filepath.Join(driDir, "renderD200"), intel, false},
		{"absent vendor falls back", "nvidia", intel, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, matched, ok := SelectRenderNode(nodes, tc.hint)
			if !ok || node.Path != tc.want || matched != tc.hintMatched {
				t.Fatalf("SelectRenderNode(%q) = %q matched=%v ok=%v, want %q matched=%v", tc.hint, node.Path, matched, ok, tc.want, tc.hintMatched)
			}
		})
	}
	amdOnly := []RenderNode{{Path: "/dev/dri/renderD128", Vendor: "nvidia"}, {Path: "/dev/dri/renderD129", Vendor: "amd"}}
	if node, _, _ := SelectRenderNode(amdOnly, ""); node.Path != "/dev/dri/renderD129" {
		t.Fatalf("AMD is not preferred over NVIDIA by default: %q", node.Path)
	}
	if _, _, ok := SelectRenderNode(nil, ""); ok {
		t.Fatal("no nodes still selected one")
	}
	if !RenderNodeHint(" /dev/dri/renderD130") || !RenderNodeHint("Intel") || RenderNodeHint("qsv") || RenderNodeHint("") {
		t.Fatal("RenderNodeHint misclassified a stored device")
	}
}

// useRenderNode installs node for one test and restores the default after.
func useRenderNode(t *testing.T, node string) {
	t.Helper()
	SetRenderNode(node)
	t.Cleanup(func() { SetRenderNode("") })
}

func TestSelectedRenderNodeDrivesProbeToneMapAndEncode(t *testing.T) {
	const node = "/dev/dri/renderD129"
	useRenderNode(t, node)
	wantQSVInit := []string{"-init_hw_device", "qsv=bfqsv:hw,child_device=" + node}

	for _, encoder := range []string{"h264_qsv", "hevc_qsv"} {
		if got := hwAccelProbeArgs(encoder); !slices.Equal(got[:2], wantQSVInit) {
			t.Fatalf("%s probe argv = %v, want it to lead with %v", encoder, got, wantQSVInit)
		}
	}
	if got := hwAccelProbeArgs("h264_vaapi"); !slices.Contains(got, node) || slices.Contains(got, "qsv=bfqsv:hw,child_device="+node) {
		t.Fatalf("VAAPI probe argv = %v, want -vaapi_device %s and no QSV init", got, node)
	}
	if got := hwAccelProbeArgs("h264_nvenc"); slices.Contains(got, "-init_hw_device") {
		t.Fatalf("NVENC probe argv initialises a DRM device: %v", got)
	}

	qsv := Rendition{Height: 720}.withHWAccel(HWAccelQSV)
	if got := qsv.inputArgs(); !slices.Equal(got, wantQSVInit) {
		t.Fatalf("QSV encode inputArgs = %v, want %v", got, wantQSVInit)
	}
	vaapi := Rendition{Height: 720}.withHWAccel(HWAccelVAAPI)
	if got, want := vaapi.inputArgs(), []string{"-vaapi_device", node}; !slices.Equal(got, want) {
		t.Fatalf("VAAPI encode inputArgs = %v, want %v", got, want)
	}

	// A VAAPI tone-map beside a QSV encode: both devices on the selected node,
	// one QSV device for the encoder to pick up.
	hdr := Rendition{Height: 720, ColorTransfer: "smpte2084"}
	mixed := hdr.withHWAccel(HWAccelQSV).withToneMapAccel(HWAccelVAAPI).inputArgs()
	if !slices.Contains(mixed, "vaapi=va:"+node) || !slices.Contains(mixed, "qsv=bfqsv:hw,child_device="+node) {
		t.Fatalf("VAAPI tone-map + QSV encode inputArgs = %v, want both devices on %s", mixed, node)
	}
	// A QSV tone-map already created the QSV device on the node; a second one
	// would leave the encoder unable to choose.
	shared := hdr.withHWAccel(HWAccelQSV).withToneMapAccel(HWAccelQSV).inputArgs()
	if !slices.Contains(shared, "qsv=hw:hw,child_device="+node) || slices.Contains(shared, "qsv=bfqsv:hw,child_device="+node) {
		t.Fatalf("QSV tone-map + QSV encode inputArgs = %v, want only the tone-map QSV device on %s", shared, node)
	}
}
