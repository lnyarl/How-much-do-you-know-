package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultStorePathPreservesExistingKnowledge(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "penumbra", "knowledge.db")
	legacy := filepath.Join(base, "a", "knowledge.db")
	path, err := defaultStorePath(base)
	if err != nil || path != current {
		t.Fatalf("new store: %q %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("existing store"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err = defaultStorePath(base)
	if err != nil || path != legacy {
		t.Fatalf("existing knowledge not selected: %q %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(current), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("new store"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err = defaultStorePath(base)
	if err != nil || path != current {
		t.Fatalf("current store not preferred: %q %v", path, err)
	}
	content, err := os.ReadFile(legacy)
	if err != nil || string(content) != "existing store" {
		t.Fatal("legacy store changed")
	}
}
