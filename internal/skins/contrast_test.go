package skins

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestContrastRatioWCAGVectors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		fg, bg string
		want   float64
		tol    float64
	}{
		{"black on white is 21:1", "#000000", "#ffffff", 21.0, 1e-9},
		{"white on white is 1:1", "#ffffff", "#ffffff", 1.0, 1e-9},
		{"black on black is 1:1", "#000000", "#000000", 1.0, 1e-9},
		// WCAG boundary pair: #777 just fails AA, #767676 just passes.
		{"gray 777 on white ~4.48", "#777777", "#ffffff", 4.48, 0.02},
		{"gray 767676 on white ~4.54", "#767676", "#ffffff", 4.54, 0.02},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ContrastRatio(c.fg, c.bg)
			if err != nil {
				t.Fatalf("ContrastRatio(%q,%q) error: %v", c.fg, c.bg, err)
			}
			if !approx(got, c.want, c.tol) {
				t.Fatalf("ContrastRatio(%q,%q) = %.4f, want ~%.4f", c.fg, c.bg, got, c.want)
			}
		})
	}
}

func TestContrastRatioSymmetric(t *testing.T) {
	t.Parallel()
	a, err := ContrastRatio("#123456", "#abcdef")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ContrastRatio("#abcdef", "#123456")
	if err != nil {
		t.Fatal(err)
	}
	if !approx(a, b, 1e-12) {
		t.Fatalf("not symmetric: %.6f vs %.6f", a, b)
	}
}

func TestMeetsContrastAABoundary(t *testing.T) {
	t.Parallel()
	if ok, _ := MeetsContrast("#777777", "#ffffff", WCAGAANormal); ok {
		t.Fatal("#777777 on white should FAIL AA-normal (4.5)")
	}
	if ok, _ := MeetsContrast("#767676", "#ffffff", WCAGAANormal); !ok {
		t.Fatal("#767676 on white should PASS AA-normal (4.5)")
	}
	// Low-contrast hard-role choice (the case the validator must reject).
	if ok, _ := MeetsContrast("#101216", "#171a20", WCAGAANormal); ok {
		t.Fatal("near-equal dark colors should FAIL AA-normal")
	}
}

func TestParsingFormsAndErrors(t *testing.T) {
	t.Parallel()
	// "#" optional, case-insensitive, alpha dropped (8-digit == its 6-digit prefix).
	withHash, _ := Luminance("#FFFFFF")
	noHash, _ := Luminance("ffffff")
	withAlpha, _ := Luminance("#ffffffcc")
	if !approx(withHash, noHash, 1e-12) || !approx(withHash, withAlpha, 1e-12) {
		t.Fatalf("parse forms disagree: %.6f / %.6f / %.6f", withHash, noHash, withAlpha)
	}
	for _, bad := range []string{"", "#fff", "#12345", "#gggggg", "12345678901"} {
		if _, err := Luminance(bad); err == nil {
			t.Fatalf("Luminance(%q) expected error, got nil", bad)
		}
	}
}
