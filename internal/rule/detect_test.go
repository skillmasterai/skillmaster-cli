package rule

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// detection is the per-client answer the report needs; the helpers below keep each case to one
// line.
func detectionFor(t *testing.T, detections []Detection, agent string) (Detection, bool) {
	t.Helper()
	for _, detection := range detections {
		if detection.Agent == agent {
			return detection, true
		}
	}
	return Detection{}, false
}

func isolatedClients(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	return home
}

func TestDetectAnswersPerClient(t *testing.T) {
	home := isolatedClients(t)
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), Block())
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), []byte("没有守则\n"))

	detections := Detect(home)
	if len(detections) != 2 {
		t.Fatalf("detections = %+v, want two", detections)
	}
	claude, _ := detectionFor(t, detections, "claude-code")
	if !claude.Present {
		t.Error("claude-code has the rule but was reported absent")
	}
	codex, _ := detectionFor(t, detections, "codex")
	if codex.Present {
		t.Error("codex has no rule but was reported present")
	}
}

// **The file the client reads preferentially is the only one that counts.** With an override file
// present, a rule in the ordinary file is not what Codex loads.
func TestDetectPrefersTheCodexOverride(t *testing.T) {
	home := isolatedClients(t)
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), Block())
	write(t, filepath.Join(home, ".codex", "AGENTS.override.md"), []byte("没有守则\n"))
	codex, ok := detectionFor(t, Detect(home), "codex")
	if !ok {
		t.Fatal("codex was not detected at all")
	}
	if codex.Present {
		t.Error("the ordinary file's rule was counted while the override is in force")
	}
}

func TestDetectHonoursTheCodexHome(t *testing.T) {
	home := isolatedClients(t)
	elsewhere := filepath.Join(home, "codex-home")
	write(t, filepath.Join(elsewhere, "AGENTS.md"), Block())
	t.Setenv("CODEX_HOME", elsewhere)
	codex, ok := detectionFor(t, Detect(home), "codex")
	if !ok || !codex.Present {
		t.Fatalf("codex = %+v, ok = %v; want the rule at CODEX_HOME", codex, ok)
	}
}

// **WorkBuddy keeps one memory file per signed-in user,** and the root file is only a fallback: a
// rule left there after login is one the app no longer reads.
func TestDetectReadsTheWorkBuddyUserDirectory(t *testing.T) {
	home := isolatedClients(t)
	root := filepath.Join(home, ".workbuddy")
	write(t, filepath.Join(root, "MEMORY.md"), Block())
	write(t, filepath.Join(root, "user-1-personal", "MEMORY.md"), []byte("没有守则\n"))
	workbuddy, ok := detectionFor(t, Detect(home), "workbuddy")
	if !ok {
		t.Fatal("workbuddy was not detected")
	}
	if workbuddy.Present {
		t.Error("the root fallback was counted while a user directory exists")
	}
	if !filepath.IsAbs(workbuddy.Path) || filepath.Base(filepath.Dir(workbuddy.Path)) != "user-1-personal" {
		t.Errorf("path = %q, want the user directory's file", workbuddy.Path)
	}
}

func TestDetectFallsBackToTheWorkBuddyRoot(t *testing.T) {
	home := isolatedClients(t)
	write(t, filepath.Join(home, ".workbuddy", "MEMORY.md"), Block())
	workbuddy, ok := detectionFor(t, Detect(home), "workbuddy")
	if !ok || !workbuddy.Present {
		t.Fatalf("workbuddy = %+v, ok = %v; want the root file counted", workbuddy, ok)
	}
}

func TestDetectCountsACursorRuleOnlyWhenItIsReadable(t *testing.T) {
	home := isolatedClients(t)
	write(t, filepath.Join(home, ".cursor", "rules", "ignored.mdc"), Block())
	cursor, ok := detectionFor(t, Detect(home), "cursor")
	if !ok {
		t.Fatal("cursor was not detected")
	}
	if cursor.Present {
		t.Error("a .mdc without frontmatter was counted")
	}
	write(t, filepath.Join(home, ".cursor", "rules", "loaded.mdc"),
		[]byte("---\nalwaysApply: true\n---\n"+string(Block())))
	cursor, _ = detectionFor(t, Detect(home), "cursor")
	if !cursor.Present {
		t.Error("a .mdc with frontmatter was not counted")
	}
}

// **A dotfiles symlink is coverage.** The editor reads the target, so the report answers about the
// target too (v94's default-path answer did the same).
func TestDetectFollowsASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	home := isolatedClients(t)
	target := filepath.Join(home, "dotfiles", "CLAUDE.md")
	write(t, target, Block())
	link := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	claude, ok := detectionFor(t, Detect(home), "claude-code")
	if !ok || !claude.Present {
		t.Fatalf("claude-code = %+v, ok = %v; want the linked rule counted", claude, ok)
	}
}

// **A bound user directory wins even before it has a memory file.** WorkBuddy reads only
// `user-<id>-personal/MEMORY.md` once a user is signed in; a rule left in the root file is one the
// app no longer reads, and counting it would report coverage that is not there.
func TestDetectDoesNotFallBackOnceAWorkBuddyUserDirectoryExists(t *testing.T) {
	home := isolatedClients(t)
	root := filepath.Join(home, ".workbuddy")
	write(t, filepath.Join(root, "MEMORY.md"), Block())
	if err := os.MkdirAll(filepath.Join(root, "user-1-personal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectionFor(t, Detect(home), "workbuddy"); ok {
		t.Error("the root file was counted while a user directory exists")
	}
}

// **A file named `user-*` is not a signed-in user.** Only directories bind WorkBuddy; the root
// `MEMORY.md` stays the answer when nothing else is there.
func TestDetectIgnoresAUserNamedFile(t *testing.T) {
	home := isolatedClients(t)
	root := filepath.Join(home, ".workbuddy")
	write(t, filepath.Join(root, "MEMORY.md"), Block())
	if err := os.WriteFile(filepath.Join(root, "user-notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	workbuddy, ok := detectionFor(t, Detect(home), "workbuddy")
	if !ok || !workbuddy.Present {
		t.Fatalf("workbuddy = %+v, ok = %v; want the root file counted", workbuddy, ok)
	}
}

// No file, no row: a client this build knows but which is not on the machine is not "absent", it is
// simply not there.
func TestDetectLeavesOutClientsWithNoFile(t *testing.T) {
	home := isolatedClients(t)
	if detections := Detect(home); len(detections) != 0 {
		t.Errorf("detections = %+v, want none", detections)
	}
	if detections := Detect(""); len(detections) != 0 {
		t.Errorf("an empty home produced %+v", detections)
	}
}

// A directory is not a rule file (a person can have `~/.claude/CLAUDE.md` as a directory by
// mistake, and reading it would fail every command).
func TestDetectIgnoresNonRegularFiles(t *testing.T) {
	home := isolatedClients(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectionFor(t, Detect(home), "claude-code"); ok {
		t.Error("a directory was detected as an instruction file")
	}
}
