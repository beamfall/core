//go:build beamfall_appstore

package compat

// appStoreBuild forces the route group off: App Store binaries carry no
// compatibility surface (ADR-0171 §2.2, §3).
const appStoreBuild = true
