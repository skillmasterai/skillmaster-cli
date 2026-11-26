package rule

import (
	"os"
	"path/filepath"
	"testing"
)

// hintHome points HOME and the two override variables at a fresh tree, so a test's default-file
// scan never reads the machine's real instruction files.
func hintHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	return home
}

// **The ledger is not the answer.** A record can point at a file someone emptied, and a default
// file can hold an old rule nobody recorded; NeedHint reports both from disk.
func TestNeedHintReadsTheFilesNotJustTheLedger(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	recorded := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(recorded, []byte("别的笔记\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: Version, Files: []File{
		{Path: recorded, Agent: "claude-code", RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintRecordedMissing || h.Path != recorded || h.Agent != "claude-code" {
		t.Fatalf("hint = %+v; want a recorded-missing hint for %s", h, recorded)
	}
}

// Two records that disagree: one file kept the current rule, the other was emptied. The gap is
// reported — a healthy file elsewhere does not make the emptied one's record true.
func TestNeedHintReportsTheGapBesideAHealthyRecord(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	healthy := filepath.Join(dir, "CLAUDE.md")
	emptied := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(healthy, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(emptied, []byte("别的笔记\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: Version, Files: []File{
		{Path: healthy, Agent: "claude-code", RuleVersion: Version},
		{Path: emptied, Agent: "codex", RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintRecordedMissing || h.Path != emptied || h.Agent != "codex" {
		t.Fatalf("hint = %+v; want the emptied record named", h)
	}
}

// The recorded counterpart of a hand edit: the file still carries the heading, so it is not
// missing, but it is not a rule this build can count either. The hint names it and starts at
// `install --check`, because `install --file` refuses an edited file.
func TestNeedHintNamesARecordedFileSomeoneEdited(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	edited := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(edited, []byte("SkillMaster 守则：\n我自己改过的守则\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, Files: []File{
		{Path: edited, Agent: "claude-code", RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintRecordedMissing || h.Path != edited {
		t.Fatalf("hint = %+v; want the edited record named", h)
	}
}

// **An edited rule nobody recorded is the person's own text.** `install` refuses to overwrite it,
// so the notice must not call the machine ruleless (technical design, open question 2).
func TestNeedHintTreatsAnEditedDefaultRuleAsPresent(t *testing.T) {
	dir := tempDir(t)
	home := hintHome(t)
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "CLAUDE.md"), []byte("SkillMaster 守则：\n我自己改过的守则\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintNone {
		t.Fatalf("hint = %+v; want none for a file the person edited", h)
	}
}

// An edit with an exact old copy beside it is still the person's edit: `Check` and `merge` read it
// as modified (and refuse to write), so the notice must not call it 「旧版」 and point at install.
func TestNeedHintTreatsAnEditedDefaultWithAnOldCopyAsPresent(t *testing.T) {
	dir := tempDir(t)
	home := hintHome(t)
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "SkillMaster 守则：\n我自己改过的守则\n\n" + string(legacyBlocks[0])
	if err := os.WriteFile(filepath.Join(claude, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintNone {
		t.Fatalf("hint = %+v; want none for an edited file with an old copy", h)
	}
}

// A `.mdc` holding only an old rule is ignored for the same reason the current one is: the editor
// loads nothing without frontmatter, and the fix is the frontmatter, not 「登记并更新」.
func TestNeedHintReportsAnIgnoredCursorFileWithAnOldRule(t *testing.T) {
	dir := tempDir(t)
	home := hintHome(t)
	rules := filepath.Join(home, ".cursor", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "skillmaster.mdc"), legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintUnreadableDefault || h.Agent != "cursor" {
		t.Fatalf("hint = %+v; want an unreadable-default hint for cursor", h)
	}
}

func TestNeedHintReportsALegacyDefaultFile(t *testing.T) {
	dir := tempDir(t)
	home := hintHome(t)
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "AGENTS.md"), legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	h := NeedHint(dir)
	if h.Kind != HintOutdatedDefault || h.Agent != "codex" {
		t.Fatalf("hint = %+v; want an outdated-default hint for codex", h)
	}
}

// A Cursor rule file without frontmatter is loaded by nothing, so the bytes are not a rule. It gets
// its own hint because the fix is to add the frontmatter, not to install the block again.
func TestNeedHintReportsAnIgnoredCursorRuleFile(t *testing.T) {
	dir := tempDir(t)
	home := hintHome(t)
	rules := filepath.Join(home, ".cursor", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "skillmaster.mdc"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NeedHint(dir)
	if h.Kind != HintUnreadableDefault || h.Agent != "cursor" {
		t.Fatalf("hint = %+v; want an unreadable-default hint for cursor", h)
	}
}

// The same file, recorded: `FileHasRule` says no, so the recorded line names it — the fix there
// starts at `install --check`, whose 「读不到」 carries the frontmatter remedy.
func TestNeedHintNamesARecordedFileWithoutFrontmatter(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	ruleFile := filepath.Join(dir, "skillmaster.mdc")
	if err := os.WriteFile(ruleFile, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, Files: []File{
		{Path: ruleFile, Agent: "cursor", RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintRecordedMissing || h.Path != ruleFile {
		t.Fatalf("hint = %+v; want the frontmatter-less record named", h)
	}
}

func TestNeedHintSkipsARecordWithNoPath(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	if err := SaveState(dir, State{Schema: 1, Files: []File{{Agent: "claude-code"}}}); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintNothingInstalled {
		t.Fatalf("hint = %+v; want nothing-installed, not a blank path", h)
	}
}

// **A ledger that will not parse is not a machine without a rule.** Saying so would be false, and
// marking the version would then spend this machine's one notice on a guess.
func TestNeedHintSaysNothingWhenTheLedgerCannotBeRead(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	if err := os.WriteFile(StatePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintUnreadable {
		t.Fatalf("hint = %+v; want unreadable", h)
	}
}

func TestNeedHintFallsBackToNothingInstalled(t *testing.T) {
	dir := tempDir(t)
	hintHome(t)
	if h := NeedHint(dir); h.Kind != HintNothingInstalled {
		t.Fatalf("hint = %+v; want nothing-installed", h)
	}
}

func TestNeedHintStaysQuietWhenTheRuleIsCurrent(t *testing.T) {
	dir := tempDir(t)
	home := hintHome(t)
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "CLAUDE.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := NeedHint(dir); h.Kind != HintNone {
		t.Fatalf("hint = %+v; want none", h)
	}
}

func TestHintStateRoundTrip(t *testing.T) {
	dir := tempDir(t)
	if got := HintedVersion(dir); got != 0 {
		t.Fatalf("fresh state = %d, want 0", got)
	}
	if err := MarkHinted(dir, 3); err != nil {
		t.Fatal(err)
	}
	if got := HintedVersion(dir); got != 3 {
		t.Fatalf("state = %d, want 3", got)
	}
}
