// Package skins holds server-side logic for user-authored color Skins:
// a Skin is a per-profile, color-only override of semantic roles, validated
// server-authoritatively before it is persisted.
//
// This file is the WCAG-2.2 contrast math used to enforce the accessibility
// floor on hard roles. It is a direct port of the canonical formula
// in beamfall-design-system/tokens/lib.mjs (srgbToLinear / luminance /
// contrastRatio); the two MUST agree, so any change here changes there too.
//
// TODO(SKIN-2/3): wire Skin validation into store/graph/conformance. Until then
// this package is an intentional leaf: contrast math only, no persistence or
// client-visible surface.
package skins

import (
	"fmt"
	"math"
	"strings"
)

// WCAG 2.2 contrast thresholds. The caller picks the threshold per role; the
// hard-role floor for color roles is AA-normal.
const (
	WCAGAANormal = 4.5 // normal text / non-large UI
	WCAGAALarge  = 3.0 // large text / UI components
)

// srgbToLinear converts one 0–1 sRGB channel to linear light (WCAG 2.2).
func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// Luminance returns the WCAG 2.2 relative luminance of an "#rrggbb" (or
// "#rrggbbaa", alpha ignored) color. The "#" is optional; case-insensitive.
func Luminance(hex string) (float64, error) {
	r, g, b, err := parseRGB(hex)
	if err != nil {
		return 0, err
	}
	lr := srgbToLinear(float64(r) / 255)
	lg := srgbToLinear(float64(g) / 255)
	lb := srgbToLinear(float64(b) / 255)
	return 0.2126*lr + 0.7152*lg + 0.0722*lb, nil
}

// ContrastRatio returns the WCAG 2.2 contrast ratio between two colors, in
// [1, 21]. It is symmetric in its arguments.
func ContrastRatio(fg, bg string) (float64, error) {
	l1, err := Luminance(fg)
	if err != nil {
		return 0, fmt.Errorf("fg: %w", err)
	}
	l2, err := Luminance(bg)
	if err != nil {
		return 0, fmt.Errorf("bg: %w", err)
	}
	hi, lo := math.Max(l1, l2), math.Min(l1, l2)
	return (hi + 0.05) / (lo + 0.05), nil
}

// MeetsContrast reports whether fg-on-bg meets the given minimum ratio.
func MeetsContrast(fg, bg string, min float64) (bool, error) {
	ratio, err := ContrastRatio(fg, bg)
	if err != nil {
		return false, err
	}
	return ratio >= min, nil
}

// parseRGB extracts the 8-bit r,g,b bytes from a "#rrggbb"/"#rrggbbaa" string.
// Alpha, if present, is dropped: WCAG contrast is defined on opaque colors, and
// alpha compositing against an unknown backdrop is out of scope for this floor.
func parseRGB(hex string) (r, g, b uint8, err error) {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(hex), "#"))
	if len(h) != 6 && len(h) != 8 {
		return 0, 0, 0, fmt.Errorf("skins: invalid hex color %q: want #rrggbb or #rrggbbaa", hex)
	}
	var v [3]uint8
	for i := 0; i < 3; i++ {
		n, perr := parseHexByte(h[i*2 : i*2+2])
		if perr != nil {
			return 0, 0, 0, fmt.Errorf("skins: invalid hex color %q: %w", hex, perr)
		}
		v[i] = n
	}
	return v[0], v[1], v[2], nil
}

func parseHexByte(s string) (uint8, error) {
	var n int
	for i := 0; i < len(s); i++ {
		d := hexDigit(s[i])
		if d < 0 {
			return 0, fmt.Errorf("non-hex digit %q", string(s[i]))
		}
		n = n*16 + d
	}
	return uint8(n), nil
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return -1
	}
}
