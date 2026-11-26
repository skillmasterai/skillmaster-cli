package rule

import (
	"os"
	"path/filepath"
	"testing"
)

// **A fallback, not an "or".** Once WorkBuddy has a per-user memory file it reads only that one;
// a rule left in the root file from before login is one the agent no longer sees.
func TestWorkBuddyPrefersThePerUserFileOnceItExists(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".workbuddy")
	userDir := filepath.Join(root, "user-773f-personal")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "MEMORY.md"), []byte("别的记忆\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); present || !known {
		t.Fatalf("present=%v known=%v; the root file must not count once a user file exists", present, known)
	}
	if err := os.WriteFile(filepath.Join(userDir, "MEMORY.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, _ := DefaultPresent(home); !present {
		t.Fatal("the per-user file was not read")
	}
}
