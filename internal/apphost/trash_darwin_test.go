package apphost

import (
	"os"
	"path/filepath"
	"testing"
)

// On a macOS older than 15, without its trash command, the item is moved
// into the Trash by hand.
func TestATrashedItemGetsANameNotTaken(t *testing.T) {
	dir := t.TempDir()
	trash := filepath.Join(dir, "Trash")
	must(t, os.Mkdir(trash, 0o755))
	for i := 0; i < 2; i++ {
		item := filepath.Join(dir, "draft.txt")
		must(t, os.WriteFile(item, []byte("x"), 0o644))
		into, err := moveToTrash(item, trash)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(into); err != nil || filepath.Dir(into) != trash {
			t.Fatalf("trashed into %q: %v", into, err)
		}
	}
	items, _ := os.ReadDir(trash)
	if len(items) != 2 {
		t.Fatalf("%d items in the Trash, want 2", len(items))
	}
}
