package artwork

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// shelfMosaicRules adds a "tag" kind to the shelf fixture: a grouping that
// derives a mosaic from the boxes tagged with it, then from items it tags.
func shelfMosaicRules() []EntityPosterRule {
	return append(shelfRules(), EntityPosterRule{PluginID: "shelf", Kind: "tag", Mosaic: true, Sources: []EntityPosterSource{
		{Relation: "tagged", FromKind: "box"},
		{Relation: "tags"},
	}})
}

// solidCover is a PNG cover of one colour; width varies the byte size so
// distinct covers never share a size.
func solidCover(t *testing.T, c color.RGBA, width int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, width*3/2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func newMosaicShelf(t *testing.T) *shelf {
	t.Helper()
	s := newShelf(t)
	s.svc.SetEntityPosterRules(shelfMosaicRules())
	return s
}

func (s *shelf) tags(tag store.EntityID, item store.MediaID) {
	s.t.Helper()
	s.relate(store.EntityRelation{FromEntityID: tag, Name: "tags", ToMediaID: item})
}

func (s *shelf) decodePoster(id store.EntityID) image.Image {
	s.t.Helper()
	data := s.poster(id)
	img, _, err := image.Decode(bytes.NewReader([]byte(data)))
	if err != nil {
		s.t.Fatalf("%s poster is not an image: %v", id, err)
	}
	return img
}

// near reports whether c is within a JPEG's error of want.
func near(c color.Color, want color.RGBA) bool {
	r, g, b, _ := c.RGBA()
	d := func(a uint32, b uint8) bool {
		diff := int(a>>8) - int(b)
		return diff > -24 && diff < 24
	}
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

var (
	red     = color.RGBA{R: 0xd0, A: 0xff}
	green   = color.RGBA{G: 0xd0, A: 0xff}
	blue    = color.RGBA{B: 0xd0, A: 0xff}
	yellow  = color.RGBA{R: 0xd0, G: 0xd0, A: 0xff}
	magenta = color.RGBA{R: 0xd0, B: 0xd0, A: 0xff}
)

// quarters samples the centre of each mosaic tile, row by row.
func quarters(img image.Image) []color.Color {
	var tiles []color.Color
	for row := range 2 {
		for col := range 2 {
			tiles = append(tiles, img.At(col*mosaicCellWidth+mosaicCellWidth/2, row*mosaicCellHeight+mosaicCellHeight/2))
		}
	}
	return tiles
}

func hasTile(tiles []color.Color, want color.RGBA) bool {
	for _, tile := range tiles {
		if near(tile, want) {
			return true
		}
	}
	return false
}

// A grouping with four distinct covers among its sources gets a 2x2 mosaic:
// container Entities first, then its own Media, skipping a member whose
// cover repeats one already chosen.
func TestSweepComposesMosaicForGroupingEntity(t *testing.T) {
	s := newMosaicShelf(t)
	s.entity("entity-tag", "tag")
	s.entity("entity-box", "box")
	s.holds("entity-box", s.item("held", solidCover(t, red, 40)), 1)
	s.relate(store.EntityRelation{FromEntityID: "entity-box", Name: "tagged", ToEntityID: "entity-tag"})
	// A copy of the box's cover on a tagged item adds nothing.
	s.tags("entity-tag", s.item("repeat", solidCover(t, red, 40)))
	s.tags("entity-tag", s.item("b", solidCover(t, green, 42)))
	s.tags("entity-tag", s.item("c", solidCover(t, blue, 44)))
	s.tags("entity-tag", s.item("d", solidCover(t, yellow, 46)))

	s.runSweep()

	if !s.svc.EntityPosterInherited("entity-tag") {
		t.Fatal("mosaic is not marked inherited")
	}
	img := s.decodePoster("entity-tag")
	if got := img.Bounds().Size(); got != image.Pt(2*mosaicCellWidth, 2*mosaicCellHeight) {
		t.Fatalf("mosaic size = %v", got)
	}
	// The box's cover leads; the tagged items follow in the store's order.
	tiles := quarters(img)
	if !near(tiles[0], red) {
		t.Errorf("top-left tile = %v, want the box's cover", tiles[0])
	}
	for _, want := range []color.RGBA{green, blue, yellow} {
		if !hasTile(tiles, want) {
			t.Errorf("no tile is about %v", want)
		}
	}
	if !near(img.At(mosaicCellWidth, mosaicCellHeight/2), mosaicGutterColor) {
		t.Errorf("no gutter between tiles: %v", img.At(mosaicCellWidth, mosaicCellHeight/2))
	}
}

// A steady-state sweep leaves the mosaic alone; a replaced source cover
// recomposes it, and with fewer than four distinct covers the grouping falls
// back to a copy of the first.
func TestSweepMosaicIsIdempotentAndFollowsSources(t *testing.T) {
	s := newMosaicShelf(t)
	s.entity("entity-tag", "tag")
	ids := []store.MediaID{
		s.item("a", solidCover(t, red, 40)),
		s.item("b", solidCover(t, green, 42)),
		s.item("c", solidCover(t, blue, 44)),
		s.item("d", solidCover(t, yellow, 46)),
	}
	for _, id := range ids {
		s.tags("entity-tag", id)
	}
	s.runSweep()
	path := s.svc.EntityPosterPath("entity-tag")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	s.runSweep()

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("second sweep rewrote an unchanged mosaic")
	}

	s.cover(ids[0], solidCover(t, magenta, 48))
	s.runSweep()
	if tiles := quarters(s.decodePoster("entity-tag")); !hasTile(tiles, magenta) || hasTile(tiles, red) {
		t.Fatalf("tiles after replacing a source = %v, want about %v in place of %v", tiles, magenta, red)
	}

	single := solidCover(t, red, 50)
	for _, id := range ids {
		s.cover(id, single)
	}
	s.runSweep()
	s.wantPoster("entity-tag", single, true)
}

// A grouping's own poster is never replaced by a mosaic.
func TestSweepKeepsGroupingsOwnPoster(t *testing.T) {
	s := newMosaicShelf(t)
	s.entity("entity-tag", "tag")
	for i, c := range []color.RGBA{red, green, blue, yellow} {
		s.tags("entity-tag", s.item(string(rune('a'+i)), solidCover(t, c, 40+2*i)))
	}
	if err := s.svc.ImportEntityPoster(t.Context(), "entity-tag", []byte("own-poster")); err != nil {
		t.Fatal(err)
	}

	s.runSweep()

	s.wantPoster("entity-tag", "own-poster", false)
}
