package main

import (
	"github.com/beamfall/core/internal/plugin"
	audiobook "github.com/beamfall/plugin-audiobook"
	familyarchive "github.com/beamfall/plugin-familyarchive"
	filmtv "github.com/beamfall/plugin-filmtv"
	karaoke "github.com/beamfall/plugin-karaoke"
	music "github.com/beamfall/plugin-music"
	photos "github.com/beamfall/plugin-photos"
	podcasts "github.com/beamfall/plugin-podcasts"
)

// firstPartyBundles lists the first-party plugin bundles compiled into the
// server binary. cmd is the sole composer of external plugin modules
// (ARCHITECTURE §2); every build, including App Store builds, ships the same
// set.
func firstPartyBundles() []plugin.Bundle {
	return []plugin.Bundle{
		filmtv.Bundle(),
		music.Bundle(),
		podcasts.Bundle(),
		photos.Bundle(),
		audiobook.Bundle(),
		familyarchive.Bundle(),
		karaoke.Bundle(),
	}
}
