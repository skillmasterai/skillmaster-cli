package rule

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// **Two leftovers are two leftovers.** A file can collect them across versions (`prompt print`
// appended, an uninstall left the append, the fallback pasted another). Removing only the first
// would leave the agent two old rule sets beside the new one — the state cleanup exists to end.
func TestInstallRemovesEveryLeftoverOldBlock(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	content := string(legacyBlocks[0]) + "\n" + string(Block()) + "\n" + string(legacyBlocks[1])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	} else if res.Action != "cleaned" {
		t.Errorf("action = %q, want cleaned", res.Action)
	}
	blob := readFile(t, target)
	if bytes.Count(blob, []byte("SkillMaster 守则：")) != 1 {
		t.Errorf("the rule appears %d times, want 1:\n%s", bytes.Count(blob, []byte("SkillMaster 守则：")), blob)
	}
	for _, old := range [][]byte{legacyBlocks[0], legacyBlocks[1]} {
		if bytes.Contains(blob, old) {
			t.Errorf("an old block is still there:\n%s", blob)
		}
	}
}

// **An edited rule beside an exact old copy is still a conflict.** The leftover branch must not
// answer first: replacing the old block there would leave the person's text and ours side by side,
// which is the two-rule-sets state the conflict exists to refuse.
func TestInstallRefusesAnEditedRuleBesideAnOldBlock(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	content := "SkillMaster 守则：\n我自己改过的守则\n\n" + string(legacyBlocks[0])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "", false); err == nil {
		t.Fatal("install wrote a second rule set beside an edited one")
	}
	if got := string(readFile(t, target)); got != content {
		t.Errorf("the refused install changed the file:\n%s", got)
	}
}

// The migration has the same rule: an edited rule is the person's, and recording the new version
// would stop `--check` from calling the file modified.
func TestMigrationLeavesAnEditedRuleBesideAnOldBlockAlone(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	content := "SkillMaster 守则：\n我自己改过的守则\n\n" + string(legacyBlocks[0])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 0 {
		t.Fatalf("migrated %d files, err = %v; want 0, nil", n, err)
	}
	if got := string(readFile(t, target)); got != content {
		t.Errorf("the migration changed an edited file:\n%s", got)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "modified" {
		t.Fatalf("statuses = %+v; want modified", statuses)
	}
}

// **The new rule goes where the first old block was**, not where this build's table happens to
// list the old text first.
func TestTheNewRuleGoesWhereTheFirstOldBlockWas(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	content := string(legacyBlocks[1]) + "\n" + string(legacyBlocks[0])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	} else if res.Action != "replaced" {
		t.Errorf("action = %q, want replaced", res.Action)
	}
	if blob := readFile(t, target); !bytes.HasPrefix(blob, Block()) {
		t.Errorf("the new rule is not where the first old block was:\n%q", blob[:60])
	}
}

// **A record that became a link is dropped, not written through.** `Check` calls it 「不管」 the
// moment the path changes; the remedy it prints has to work before the next rule version settles it.
func TestUndoDropsARecordWhosePathBecameALink(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "real.md")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: Version, Files: []File{
		{Path: link, Agent: "claude-code", RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	res, err := Undo(dir, link)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "dropped" {
		t.Errorf("action = %q, want dropped", res.Action)
	}
	if st, _, _ := LoadState(dir); len(st.Files) != 0 {
		t.Errorf("the record is still there: %+v", st.Files)
	}
	if got := string(readFile(t, target)); got != "x\n" {
		t.Errorf("undo wrote through the link: %q", got)
	}
}

// **Appending to a Windows file must not mix endings**, separator included.
func TestAppendingKeepsAWindowsFilesLineEndings(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, []byte("以前的笔记\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	} else if res.Action != "appended" {
		t.Errorf("action = %q, want appended", res.Action)
	}
	blob := readFile(t, target)
	if bytes.Count(blob, []byte("\n")) != bytes.Count(blob, []byte("\r\n")) {
		t.Errorf("the append mixed line endings: %d LF, %d CRLF", bytes.Count(blob, []byte("\n")), bytes.Count(blob, []byte("\r\n")))
	}
}

// **「已安装」 beside an old copy is still two rule sets.** The check says so, and the next write
// clears it — that is the contract the detail points at.
func TestCheckSaysWhenALeftoverSitsBesideTheCurrentRule(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	content := string(Block()) + "\n" + string(legacyBlocks[0])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: Version, Files: []File{
		{Path: target, Agent: "claude-code", RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "present" || !strings.Contains(statuses[0].Detail, "旧守则") {
		t.Fatalf("statuses = %+v; want present with a leftover note", statuses)
	}
}

// **A frontmatter-less .mdc is not migrated.** Writing it would report success for a rule Cursor
// still ignores, and settle a record that `--check` keeps calling 「读不到」.
func TestMigrationLeavesAnIgnoredCursorRuleAlone(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "skillmaster.mdc")
	if err := os.WriteFile(target, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 0 {
		t.Fatalf("migrated %d, err = %v; want 0, nil", n, err)
	}
	if got := readFile(t, target); !bytes.Equal(got, legacyBlocks[0]) {
		t.Errorf("the migration rewrote an ignored rule file:\n%s", got)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "ignored" {
		t.Fatalf("statuses = %+v; want ignored", statuses)
	}
}

// The current rule is not "a heading that is not an old block", even though it carries the heading.
func TestTheCurrentRuleIsNotAnEdit(t *testing.T) {
	if headingOutsideLegacy(Block()) {
		t.Error("the current rule was called an edit")
	}
}

// **A Windows file stays a Windows file.** Detection tries both endings, but writing an LF block
// into a CRLF file would leave it with two conventions.
func TestInstallKeepsAWindowsFilesLineEndings(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	crlfOld := bytes.ReplaceAll(legacyBlocks[0], []byte("\n"), []byte("\r\n"))
	content := append(append([]byte(nil), crlfOld...), []byte("tail\r\n")...)
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	}
	blob := readFile(t, target)
	if bytes.Count(blob, []byte("\n")) != bytes.Count(blob, []byte("\r\n")) {
		t.Errorf("the write mixed line endings: %d LF, %d CRLF", bytes.Count(blob, []byte("\n")), bytes.Count(blob, []byte("\r\n")))
	}
}

// The migration has the same problem and the same answer: replace one old block, remove the rest.
func TestMigrationLeavesExactlyOneRuleWhenTwoOldBlocksArePresent(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	content := string(legacyBlocks[0]) + "\n" + string(legacyBlocks[1])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIfNeeded(dir); err != nil {
		t.Fatal(err)
	}
	blob := readFile(t, target)
	if bytes.Count(blob, []byte("SkillMaster 守则：")) != 1 {
		t.Errorf("the rule appears %d times, want 1:\n%s", bytes.Count(blob, []byte("SkillMaster 守则：")), blob)
	}
	if !bytes.Contains(blob, Block()) {
		t.Error("the current rule is not in the file")
	}
}
