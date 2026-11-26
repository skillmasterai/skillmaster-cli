package rule

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// **The six words agents and docs already use** (ADR 0249): `self rule check` answers with them,
// and the exit code is the other half of the answer.
func TestCheckPathAnswersTheSixStates(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.md")
	write(t, current, Block())
	stale := filepath.Join(dir, "stale.md")
	write(t, stale, legacyBlocks[0])
	edited := filepath.Join(dir, "edited.md")
	write(t, edited, []byte("SkillMaster 守则：\n我改了一行。\n"))
	notes := filepath.Join(dir, "notes.md")
	write(t, notes, []byte("我自己的笔记，从来没有守则。\n"))
	mdc := filepath.Join(dir, "rule.mdc")
	write(t, mdc, Block())
	frontmatter := filepath.Join(dir, "loaded.mdc")
	write(t, frontmatter, []byte("---\ndescription: r\nalwaysApply: true\n---\n"+string(Block())+"x\n"))

	cases := []struct{ name, path, want, detail string }{
		{"current", current, "present", ""},
		{"legacy", stale, "legacy", "旧版守则"},
		{"edited", edited, "modified", "守则被改过"},
		{"never had the rule", notes, "absent", "没有 SkillMaster 守则"},
		{"no frontmatter", mdc, "ignored", "frontmatter"},
		{"frontmatter", frontmatter, "present", ""},
		{"missing", filepath.Join(dir, "nope.md"), "absent", "不存在"},
	}
	for _, tc := range cases {
		got, detail := CheckPath(tc.path)
		if got != tc.want {
			t.Errorf("%s: state = %q, want %q", tc.name, got, tc.want)
		}
		if tc.detail != "" && !strings.Contains(detail, tc.detail) {
			t.Errorf("%s: detail = %q, want it to mention %q", tc.name, detail, tc.detail)
		}
	}
}

// **Every shipped form answers "legacy", not "modified".** The table is the only record of the
// bytes older releases wrote; losing an entry sends a machine with that rule to rewrite a file
// that is merely stale.
func TestCheckPathRecognisesEveryLegacyBlock(t *testing.T) {
	dir := t.TempDir()
	for i, block := range legacyBlocks {
		path := filepath.Join(dir, "legacy.md")
		write(t, path, block)
		if state, detail := CheckPath(path); state != "legacy" {
			t.Errorf("legacyBlocks[%d]: state = %q (%s), want legacy", i, state, detail)
		}
	}
}

// **1.10.0 shipped v3 with `skmr login`; 1.10.1 replaces it.** The marker pins that entry: without
// it, every 1.10.0 machine would be told its rule was edited.
func TestTheRuleShippedIn1100IsRegisteredAsLegacy(t *testing.T) {
	for _, block := range legacyBlocks {
		if strings.Contains(string(block), "自己运行 `skmr login`") {
			return
		}
	}
	t.Error("the rule shipped as 1.10.0 (with `skmr login`) is not in legacyBlocks")
}

// **A .mdc without frontmatter is unreadable whatever its bytes say.** Answering "被改过" would
// send the agent to rewrite the text; the missing piece is the frontmatter.
func TestCheckPathCallsAnyFrontmatterlessMdcUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edited.mdc")
	write(t, path, []byte("SkillMaster 守则：\n我改过的一段。\n"))
	if state, _ := CheckPath(path); state != "ignored" {
		t.Errorf("state = %q, want ignored", state)
	}
}

// **A file inside a Git repository is project-level, and this command does not judge it.**
func TestCheckPathRefusesAProjectLevelFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "CLAUDE.md")
	write(t, path, Block())
	if state, detail := CheckPath(path); state != "unmanaged" || !strings.Contains(detail, "项目级") {
		t.Errorf("state = %q, detail = %q; want unmanaged/项目级", state, detail)
	}
}

// **A dotfiles checkout at `$HOME` is still the user level.** The walk stops at home before looking
// for `.git`; otherwise every default file would become "project-level" and no rule could be
// written.
func TestCheckPathTreatsHomeItselfAsUserLevel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME semantics differ")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "CLAUDE.md")
	write(t, path, Block())
	if state, detail := CheckPath(path); state != "present" {
		t.Errorf("state = %q (%s), want present", state, detail)
	}
	// A trailing separator is the same directory.
	t.Setenv("HOME", home+string(os.PathSeparator))
	if state, _ := CheckPath(path); state != "present" {
		t.Errorf("state with trailing separator = %q, want present", state)
	}
}

// **A directory symlink into a checkout is project-level.** GNU stow puts `~/.claude` there; the
// typed path does not show a repository, the resolved one does.
func TestCheckPathResolvesADirectorySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "dotfiles")
	write(t, filepath.Join(repo, ".git", "config"), []byte("[core]\n"))
	write(t, filepath.Join(repo, "claude", "CLAUDE.md"), Block())
	link := filepath.Join(root, ".claude")
	if err := os.Symlink(filepath.Join(repo, "claude"), link); err != nil {
		t.Fatal(err)
	}
	if state, detail := CheckPath(filepath.Join(link, "CLAUDE.md")); state != "unmanaged" {
		t.Errorf("state = %q (%s), want unmanaged", state, detail)
	}
}

// A path in a repository that does not exist yet is still project-level: the answer is "do not
// write here", not "nothing there, go ahead".
func TestCheckPathRefusesANonexistentProjectPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "new", "CLAUDE.md")
	if state, _ := CheckPath(missing); state != "unmanaged" {
		t.Errorf("state = %q, want unmanaged", state)
	}
}

// **A current rule beside an old copy is present, but not clean**: the agent may be reading two
// rule sets, and the detail is the only place a count can say so.
func TestCheckPathReportsALeftoverCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.md")
	write(t, path, append(append([]byte{}, legacyBlocks[0]...), Block()...))
	state, detail := CheckPath(path)
	if state != "present" {
		t.Fatalf("state = %q, want present", state)
	}
	if !strings.Contains(detail, "旧守则") {
		t.Errorf("detail = %q, want the leftover named", detail)
	}
}

// A symlink is not a file this CLI may judge: which target it points at (and whether that target is
// even on this machine) is the person's arrangement, not the rule's state.
func TestCheckPathRefusesASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.md")
	write(t, target, Block())
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if state, _ := CheckPath(link); state != "unmanaged" {
		t.Errorf("state = %q, want unmanaged", state)
	}
}

func TestFileHasRuleNeedsTheEditorToReadIt(t *testing.T) {
	dir := t.TempDir()
	with := filepath.Join(dir, "a.mdc")
	write(t, with, []byte("---\nalwaysApply: true\n---\n"+string(Block())))
	without := filepath.Join(dir, "b.mdc")
	write(t, without, Block())
	if !FileHasRule(with) {
		t.Error("a .mdc with frontmatter and the block was not counted")
	}
	if FileHasRule(without) {
		t.Error("a .mdc without frontmatter was counted")
	}
	if FileHasRule(filepath.Join(dir, "missing.md")) {
		t.Error("a missing file was counted")
	}
}

// **Only a leading `~` is expanded.** A path in the middle is a path, not a template.
func TestExpandPathExpandsOnlyTheFront(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got, err := ExpandPath("~/.claude/CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude", "CLAUDE.md"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	relative, err := ExpandPath("a/~/b")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(relative, filepath.Join("a", "~", "b")) {
		t.Errorf("a `~` in the middle was expanded: %q", relative)
	}
}

// The server accepts a slug; a person types a name.
func TestNormalizeAgent(t *testing.T) {
	cases := map[string]string{
		"Claude Code": "claude-code",
		"  cursor ":   "cursor",
		"":            "unknown",
		"!!!":         "unknown",
		"Codex":       "codex",
	}
	for in, want := range cases {
		if got := NormalizeAgent(in); got != want {
			t.Errorf("NormalizeAgent(%q) = %q, want %q", in, got, want)
		}
	}
}
