package ffmpeg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

// Colour handling for still-image derivatives (CORE-0310).
//
// A derivative is shown by clients that assume sRGB, so a wide-gamut source
// whose pixels are encoded for another colour space is converted before it is
// published. Only profiles FFmpeg's colorspace filter can reproduce exactly are
// converted: matrix/TRC RGB profiles whose colorants are the BT.709/sRGB,
// Display P3 or BT.2020 primaries and whose tone curve is sRGB, BT.709, a pure
// gamma 2.2 / 2.8, or linear. Any other profile (Adobe RGB, ProPhoto, LUT-based
// or unparseable) is left alone, and FFmpeg carries the source profile into the
// derivative so colour-managed viewers still render it correctly.

// stillColour names the source encoding the colorspace filter converts from.
// The zero value means "already sRGB, or not convertible": no conversion.
type stillColour struct {
	primaries string // colorspace filter iprimaries
	transfer  string // colorspace filter itrc
}

func (c stillColour) converts() bool {
	return c.primaries != "" && c.transfer != "" && (c.primaries != "bt709" || c.transfer != "srgb")
}

// colourFromNCLX maps HEIF/AVIF nclx (ISO/IEC 23091-2) code points. Codes the
// filter cannot reproduce (PQ, HLG, unspecified) return the zero value.
func colourFromNCLX(primaries, transfer uint16) stillColour {
	var c stillColour
	switch primaries {
	case 1:
		c.primaries = "bt709"
	case 9:
		c.primaries = "bt2020"
	case 12:
		c.primaries = "smpte432"
	default:
		return stillColour{}
	}
	switch transfer {
	case 13:
		c.transfer = "srgb"
	case 1, 6, 14, 15:
		c.transfer = "bt709"
	case 4:
		c.transfer = "gamma22"
	case 5:
		c.transfer = "gamma28"
	case 8:
		c.transfer = "linear"
	default:
		return stillColour{}
	}
	return c
}

// maxICCBytes bounds every embedded profile Beamfall parses. Matrix/TRC
// profiles are a few kilobytes; anything larger is a LUT profile, which is
// never converted anyway.
const maxICCBytes = 1 << 20

type chromaticity struct{ x, y float64 }

var (
	whiteD65 = chromaticity{0.3127, 0.3290}
	// iccD50 is the ICC profile connection space illuminant.
	iccD50 = [3]float64{0.9642, 1.0, 0.8249}
)

// iccPrimaries are the colorant sets the filter supports, adapted to D50 the way
// ICC v2/v4 matrix profiles store them.
var iccPrimaries = []struct {
	name     string
	colorant [3][3]float64 // rows: red, green, blue XYZ
}{
	{"bt709", d50Colorants([3]chromaticity{{0.640, 0.330}, {0.300, 0.600}, {0.150, 0.060}})},
	{"smpte432", d50Colorants([3]chromaticity{{0.680, 0.320}, {0.265, 0.690}, {0.150, 0.060}})},
	{"bt2020", d50Colorants([3]chromaticity{{0.708, 0.292}, {0.170, 0.797}, {0.131, 0.046}})},
}

// iccTransfers are the tone curves the filter supports, as encoded→linear
// functions (the direction an ICC TRC tag describes).
var iccTransfers = []struct {
	name string
	eval func(float64) float64
}{
	{"srgb", func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}},
	{"bt709", func(v float64) float64 {
		if v < 0.081 {
			return v / 4.5
		}
		return math.Pow((v+0.099)/1.099, 1/0.45)
	}},
	{"gamma22", func(v float64) float64 { return math.Pow(v, 2.2) }},
	{"gamma28", func(v float64) float64 { return math.Pow(v, 2.8) }},
	{"linear", func(v float64) float64 { return v }},
}

// d50Colorants returns the D50-adapted XYZ of each primary for an RGB space with
// a D65 white point, using the Bradford transform.
func d50Colorants(p [3]chromaticity) [3][3]float64 {
	xyz := func(c chromaticity) [3]float64 { return [3]float64{c.x / c.y, 1, (1 - c.x - c.y) / c.y} }
	var prim [3][3]float64 // columns are primaries
	for i, c := range p {
		v := xyz(c)
		for r := range 3 {
			prim[r][i] = v[r]
		}
	}
	w := xyz(whiteD65)
	s := mat3Mul(mat3Inv(prim), w)
	var m [3][3]float64 // RGB→XYZ(D65)
	for r := range 3 {
		for c := range 3 {
			m[r][c] = prim[r][c] * s[c]
		}
	}
	bradford := [3][3]float64{{0.8951, 0.2664, -0.1614}, {-0.7502, 1.7135, 0.0367}, {0.0389, -0.0685, 1.0296}}
	src := mat3Mul(bradford, w)
	dst := mat3Mul(bradford, iccD50)
	var scale [3][3]float64
	for i := range 3 {
		scale[i][i] = dst[i] / src[i]
	}
	adapt := mat3MulM(mat3Inv(bradford), mat3MulM(scale, bradford))
	d50 := mat3MulM(adapt, m)
	var out [3][3]float64
	for primary := range 3 {
		for r := range 3 {
			out[primary][r] = d50[r][primary]
		}
	}
	return out
}

func mat3Mul(m [3][3]float64, v [3]float64) [3]float64 {
	var out [3]float64
	for r := range 3 {
		out[r] = m[r][0]*v[0] + m[r][1]*v[1] + m[r][2]*v[2]
	}
	return out
}

func mat3MulM(a, b [3][3]float64) [3][3]float64 {
	var out [3][3]float64
	for r := range 3 {
		for c := range 3 {
			out[r][c] = a[r][0]*b[0][c] + a[r][1]*b[1][c] + a[r][2]*b[2][c]
		}
	}
	return out
}

func mat3Inv(m [3][3]float64) [3][3]float64 {
	a, b, c := m[0][0], m[0][1], m[0][2]
	d, e, f := m[1][0], m[1][1], m[1][2]
	g, h, i := m[2][0], m[2][1], m[2][2]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	return [3][3]float64{
		{(e*i - f*h) / det, (c*h - b*i) / det, (b*f - c*e) / det},
		{(f*g - d*i) / det, (a*i - c*g) / det, (c*d - a*f) / det},
		{(d*h - e*g) / det, (b*g - a*h) / det, (a*e - b*d) / det},
	}
}

// minICCBytes is an ICC header plus its tag count.
const minICCBytes = 132

// iccTag returns the bytes of one tag, or nil.
func iccTag(profile []byte, sig string) []byte {
	if len(profile) < minICCBytes {
		return nil
	}
	count := binary.BigEndian.Uint32(profile[128:])
	if count > 100 || 132+int(count)*12 > len(profile) {
		return nil
	}
	for i := range int(count) {
		entry := profile[132+i*12:]
		if string(entry[:4]) != sig {
			continue
		}
		off := int64(binary.BigEndian.Uint32(entry[4:]))
		size := int64(binary.BigEndian.Uint32(entry[8:]))
		if off+size > int64(len(profile)) || size < 8 {
			return nil
		}
		return profile[off : off+size]
	}
	return nil
}

// s15f16 decodes an ICC s15Fixed16Number (two's complement).
func s15f16(b []byte) float64 {
	return float64(int32(binary.BigEndian.Uint32(b))) / 65536 // #nosec G115 -- deliberate two's-complement reinterpretation
}

// iccColour classifies an embedded ICC profile. Only a matrix/TRC RGB display
// profile whose colorants and tone curves match a supported space returns a
// non-zero value.
func iccColour(profile []byte) stillColour {
	if len(profile) < minICCBytes || len(profile) > maxICCBytes || string(profile[16:20]) != "RGB " || string(profile[36:40]) != "acsp" {
		return stillColour{}
	}
	var colorant [3][3]float64
	for i, sig := range []string{"rXYZ", "gXYZ", "bXYZ"} {
		tag := iccTag(profile, sig)
		if len(tag) < 20 || string(tag[:4]) != "XYZ " {
			return stillColour{}
		}
		for j := range 3 {
			colorant[i][j] = s15f16(tag[8+4*j:])
		}
	}
	primaries := ""
	for _, p := range iccPrimaries {
		if colorantsMatch(colorant, p.colorant) {
			primaries = p.name
			break
		}
	}
	if primaries == "" {
		return stillColour{}
	}
	transfer := ""
	for _, sig := range []string{"rTRC", "gTRC", "bTRC"} {
		curve, ok := parseICCCurve(iccTag(profile, sig))
		if !ok {
			return stillColour{}
		}
		name := matchTransfer(curve)
		if name == "" || (transfer != "" && name != transfer) {
			return stillColour{}
		}
		transfer = name
	}
	return stillColour{primaries: primaries, transfer: transfer}
}

func colorantsMatch(a, b [3][3]float64) bool {
	for i := range 3 {
		for j := range 3 {
			if math.Abs(a[i][j]-b[i][j]) > 0.003 {
				return false
			}
		}
	}
	return true
}

func matchTransfer(curve func(float64) float64) string {
	samples := []float64{0.02, 0.05, 0.1, 0.2, 0.35, 0.5, 0.65, 0.8, 0.95}
	for _, t := range iccTransfers {
		ok := true
		for _, v := range samples {
			if math.Abs(curve(v)-t.eval(v)) > 0.004 {
				ok = false
				break
			}
		}
		if ok {
			return t.name
		}
	}
	return ""
}

// parseICCCurve turns a curv or para tag into an encoded→linear function.
func parseICCCurve(tag []byte) (func(float64) float64, bool) {
	if len(tag) < 12 {
		return nil, false
	}
	switch string(tag[:4]) {
	case "curv":
		n := int(binary.BigEndian.Uint32(tag[8:]))
		if n < 0 || len(tag) < 12+2*n {
			return nil, false
		}
		switch n {
		case 0:
			return func(v float64) float64 { return v }, true
		case 1:
			g := float64(binary.BigEndian.Uint16(tag[12:])) / 256
			return func(v float64) float64 { return math.Pow(v, g) }, true
		}
		table := make([]float64, n)
		for i := range n {
			table[i] = float64(binary.BigEndian.Uint16(tag[12+2*i:])) / 65535
		}
		return func(v float64) float64 {
			pos := v * float64(n-1)
			i := int(pos)
			if i >= n-1 {
				return table[n-1]
			}
			frac := pos - float64(i)
			return table[i]*(1-frac) + table[i+1]*frac
		}, true
	case "para":
		fn := binary.BigEndian.Uint16(tag[8:])
		counts := []int{1, 3, 4, 5, 7}
		if int(fn) >= len(counts) || len(tag) < 12+4*counts[fn] {
			return nil, false
		}
		p := make([]float64, 7)
		for i := range counts[fn] {
			p[i] = s15f16(tag[12+4*i:])
		}
		g, a, b, c, d, e, f := p[0], p[1], p[2], p[3], p[4], p[5], p[6]
		pow := func(x float64) float64 {
			if x <= 0 {
				return 0
			}
			return math.Pow(x, g)
		}
		switch fn {
		case 0:
			return pow, true
		case 1:
			return func(x float64) float64 {
				if a == 0 || x < -b/a {
					return 0
				}
				return pow(a*x + b)
			}, true
		case 2:
			return func(x float64) float64 {
				if a == 0 || x < -b/a {
					return c
				}
				return pow(a*x+b) + c
			}, true
		case 3:
			return func(x float64) float64 {
				if x < d {
					return c * x
				}
				return pow(a*x + b)
			}, true
		default:
			return func(x float64) float64 {
				if x < d {
					return c*x + f
				}
				return pow(a*x+b) + e
			}, true
		}
	}
	return nil, false
}

// buildDisplayICC returns a compact ICC v4 display profile with the given D50
// colorants and the sRGB tone curve. With the BT.709 colorants it is the sRGB
// profile every converted derivative embeds.
func buildDisplayICC(description string, colorant [3][3]float64) []byte {
	be := binary.BigEndian
	u32 := func(v uint32) []byte { b := make([]byte, 4); be.PutUint32(b, v); return b }
	fixed := func(v float64) []byte {
		return u32(uint32(int32(math.Round(v * 65536)))) // #nosec G115 -- s15Fixed16 two's complement of values in [-2, 3]
	}
	xyzTag := func(v [3]float64) []byte {
		out := append([]byte("XYZ "), 0, 0, 0, 0)
		for _, c := range v {
			out = append(out, fixed(c)...)
		}
		return out
	}
	mluc := func(text string) []byte {
		out := append([]byte("mluc"), 0, 0, 0, 0)
		out = append(out, u32(1)...)
		out = append(out, u32(12)...)
		out = append(out, 'e', 'n', 'U', 'S')
		out = append(out, u32(uint32(2*len(text)))...) // #nosec G115 -- the profile is under 1 KiB
		out = append(out, u32(28)...)
		for _, r := range text {
			out = append(out, 0, byte(r)) // #nosec G115 -- descriptions are ASCII literals
		}
		return out
	}
	// The sRGB curve as ICC parametric type 3.
	para := append([]byte("para"), 0, 0, 0, 0, 0, 3, 0, 0)
	for _, v := range []float64{2.4, 1 / 1.055, 0.055 / 1.055, 1 / 12.92, 0.04045} {
		para = append(para, fixed(v)...)
	}
	bradford := [3][3]float64{{0.8951, 0.2664, -0.1614}, {-0.7502, 1.7135, 0.0367}, {0.0389, -0.0685, 1.0296}}
	w := [3]float64{whiteD65.x / whiteD65.y, 1, (1 - whiteD65.x - whiteD65.y) / whiteD65.y}
	src, dst := mat3Mul(bradford, w), mat3Mul(bradford, iccD50)
	var scale [3][3]float64
	for i := range 3 {
		scale[i][i] = dst[i] / src[i]
	}
	chadM := mat3MulM(mat3Inv(bradford), mat3MulM(scale, bradford))
	chad := append([]byte("sf32"), 0, 0, 0, 0)
	for r := range 3 {
		for c := range 3 {
			chad = append(chad, fixed(chadM[r][c])...)
		}
	}
	type tag struct {
		sig  string
		data []byte
	}
	tags := []tag{
		{"desc", mluc(description)},
		{"cprt", mluc("No copyright, use freely")},
		{"wtpt", xyzTag(iccD50)},
		{"chad", chad},
		{"rXYZ", xyzTag(colorant[0])},
		{"gXYZ", xyzTag(colorant[1])},
		{"bXYZ", xyzTag(colorant[2])},
		{"rTRC", para},
		{"gTRC", para},
		{"bTRC", para},
	}
	table := u32(uint32(len(tags))) // #nosec G115 -- ten tags
	var body []byte
	offset := 128 + 4 + 12*len(tags)
	shared := map[string]int{}
	for _, t := range tags {
		key := string(t.data)
		off, ok := shared[key]
		if !ok {
			off = offset + len(body)
			shared[key] = off
			body = append(body, t.data...)
			for len(body)%4 != 0 {
				body = append(body, 0)
			}
		}
		table = append(table, t.sig...)
		table = append(table, u32(uint32(off))...)         // #nosec G115 -- the profile is under 1 KiB
		table = append(table, u32(uint32(len(t.data)))...) // #nosec G115 -- the profile is under 1 KiB
	}
	header := make([]byte, 128)
	total := 128 + len(table) + len(body)
	be.PutUint32(header[0:], uint32(total)) // #nosec G115 -- the profile is under 1 KiB
	be.PutUint32(header[8:], 0x04300000)
	copy(header[12:], "mntr")
	copy(header[16:], "RGB ")
	copy(header[20:], "XYZ ")
	// Creation date 2026-01-01 00:00:00, fixed so derivatives are reproducible.
	for i, v := range []uint16{2026, 1, 1, 0, 0, 0} {
		be.PutUint16(header[24+2*i:], v)
	}
	copy(header[36:], "acsp")
	for i, c := range iccD50 {
		copy(header[68+4*i:], fixed(c))
	}
	out := make([]byte, 0, total)
	out = append(out, header...)
	out = append(out, table...)
	return append(out, body...)
}

// srgbICC is embedded in every converted derivative.
var srgbICC = buildDisplayICC("sRGB", iccPrimaries[0].colorant)

var iccMarker = []byte("ICC_PROFILE\x00")

var errNotJPEG = errors.New("not a JPEG")

// jpegWithICC returns data with every APP2 ICC_PROFILE segment removed and
// profile embedded in their place, directly after SOI and any APP0 (JFIF).
func jpegWithICC(data, profile []byte) ([]byte, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errNotJPEG
	}
	if len(profile)+len(iccMarker)+4 > 0xFFFF {
		return nil, errors.New("profile too large for one APP2 segment")
	}
	seg := []byte{0xFF, 0xE2, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(2+len(iccMarker)+2+len(profile))) // #nosec G115 -- bounded to 0xFFFF just above
	seg = append(seg, iccMarker...)
	seg = append(seg, 1, 1)
	seg = append(seg, profile...)

	var out bytes.Buffer
	out.Grow(len(data) + len(seg))
	out.Write(data[:2])
	inserted := false
	pos := 2
	for {
		if pos+4 > len(data) || data[pos] != 0xFF {
			return nil, errNotJPEG
		}
		marker := data[pos+1]
		if marker == 0xFF { // fill byte
			pos++
			continue
		}
		if marker == 0xDA || marker == 0xD9 {
			if !inserted {
				out.Write(seg)
			}
			out.Write(data[pos:])
			return out.Bytes(), nil
		}
		length := int(binary.BigEndian.Uint16(data[pos+2:]))
		end := pos + 2 + length
		if length < 2 || end > len(data) {
			return nil, errNotJPEG
		}
		if !inserted && marker != 0xE0 {
			out.Write(seg)
			inserted = true
		}
		if marker != 0xE2 || !bytes.HasPrefix(data[pos+4:end], iccMarker) {
			out.Write(data[pos:end])
		}
		pos = end
	}
}

// embedICC rewrites the reserved output in place so it carries profile as its
// only ICC profile. The file is read back through the reservation's directory
// descriptor and must still be the reserved inode.
func (o *outputTemp) embedICC(profile []byte) error {
	if o == nil || o.root == nil || o.file == nil {
		return ErrPathSwapped
	}
	f, err := o.root.Open(o.name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(o.id, info) {
		return ErrPathSwapped
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPreviewBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxPreviewBytes {
		return errors.New("derivative too large to tag")
	}
	tagged, err := jpegWithICC(data, profile)
	if err != nil {
		return err
	}
	if _, err := o.file.WriteAt(tagged, 0); err != nil {
		return err
	}
	return o.file.Truncate(int64(len(tagged)))
}
