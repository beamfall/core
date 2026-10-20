package conformance

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if seededTemplate.dir != "" {
		_ = os.RemoveAll(seededTemplate.dir)
	}
	os.Exit(code)
}

var seededTemplate struct {
	once sync.Once
	dir  string
	err  error
}

// seededDataDir returns a fresh data directory holding a copy of one migrated,
// development-seeded store, ready for storetest.OpenAt. Each caller still opens
// the real store over its own bytes, but the schema migrations, which dominate a
// store boot under -race, run once per package instead of once per subtest.
func seededDataDir(t *testing.T) string {
	t.Helper()
	tpl := &seededTemplate
	tpl.once.Do(func() {
		tpl.dir, tpl.err = os.MkdirTemp("", "conformance-seeded-store-")
		if tpl.err != nil {
			return
		}
		var st *store.Store
		st, tpl.err = store.Open(context.Background(), tpl.dir, store.Options{SeedDevelopmentData: true})
		if tpl.err == nil {
			tpl.err = st.Close()
		}
	})
	if tpl.err != nil {
		t.Fatalf("seeded template store: %v", tpl.err)
	}
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(tpl.dir)); err != nil {
		t.Fatalf("copy seeded template store: %v", err)
	}
	return dst
}
