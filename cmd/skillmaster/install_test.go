package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPreviewsUntilYes(t *testing.T) {
	isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	writeFile(t, target, "原有内容\n")

	_, stderr, status := runCaptured(t, "install", "--file", target)

	if status != 2 {
		t.Fatalf("status = %d, stderr=%s", status, stderr)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("the preview does not say how to confirm:\n%s", stderr)
	}
	blob, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob) != "原有内容\n" {
		t.Errorf("a preview wrote to the file:\n%s", blob)
	}
}

func TestInstallWritesOnceAndCheckConfirmsIt(t *testing.T) {
	isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	writeFile(t, target, "原有内容\n")

	_, stderr, status := runCaptured(t, "install", "--file", target, "--agent", "claude-code", "--yes")
	if status != 0 {
		t.Fatalf("install: %d %s", status, stderr)
	}
	blob, _ := os.ReadFile(target)
	if strings.Count(string(blob), "SkillMaster 守则：") != 1 {
		t.Errorf("the rule appears %d times", strings.Count(string(blob), "SkillMaster 守则："))
	}

	stdout, _, status := runCaptured(t, "install", "--file", target, "--yes")
	if status != 0 || !strings.Contains(stdout, "已是最新") {
		t.Errorf("second run = %d %q", status, stdout)
	}

	stdout, _, status = runCaptured(t, "install", "--check")
	if status != 0 || !strings.Contains(stdout, "已安装") {
		t.Errorf("check = %d %q", status, stdout)
	}
}

func TestInstallUndoRestoresTheOriginal(t *testing.T) {
	isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "AGENTS.md")
	writeFile(t, target, "原有内容\n")
	if _, stderr, status := runCaptured(t, "install", "--file", target, "--yes"); status != 0 {
		t.Fatalf("install: %d %s", status, stderr)
	}
	if _, stderr, status := runCaptured(t, "install", "--file", target, "--undo"); status != 0 {
		t.Fatalf("undo: %d %s", status, stderr)
	}
	blob, _ := os.ReadFile(target)
	if string(blob) != "原有内容\n" {
		t.Errorf("undo did not restore the file:\n%s", blob)
	}
}

// **A failed migration says how to get out of it.** Inside an agent's sandbox the write is refused,
// and the person or agent needs to know the retry belongs outside the sandbox (seen on a real
// Cursor install, 2026-11-26).
func TestAMigrationFailureSaysToRetryOutsideTheSandbox(t *testing.T) {
	dir := isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	writeFile(t, target, releasedBlock)
	// A read-only *directory* is what makes both the backup and the rewrite fail; a read-only file
	// is replaced by the temp-file rename.
	home := filepath.Dir(target)
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	if err := rule.SaveState(dir, rule.State{Schema: 1, RuleVersion: 1,
		Files: []rule.File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	_, stderr, status := runCaptured(t, "prompt", "print")
	if status != 0 {
		t.Fatalf("a failed migration changed the exit code: %d %s", status, stderr)
	}
	if !strings.Contains(stderr, "沙箱外重跑") {
		t.Errorf("the failure did not say where to retry: %q", stderr)
	}
}

// **The person's existing skill is theirs.** v94 no longer installs the entry skill and no longer
// renames one it finds: the old loader stays exactly where it is, even on a machine that just
// received the rule (2026-11-26).
func TestInstallLeavesAnExistingEntrySkillAlone(t *testing.T) {
	isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	skill := entrySkill(t, os.Getenv("HOME"))

	if _, stderr, status := runCaptured(t, "install", "--file", target, "--yes"); status != 0 {
		t.Fatalf("install: %d %s", status, stderr)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Error("install moved the existing entry skill")
	}
	if _, err := os.Stat(skill + ".disabled"); err == nil {
		t.Error("install renamed the existing entry skill")
	}
}

// releasedBlock is the exact one-line rule cli/v1.6.0–1.8.0 wrote. The test spells it out rather
// than borrowing the catalog: this is what an upgraded machine actually has on disk.
const releasedBlock = "**铁律：任何任务都先调用 SkillMaster 这个 skill，照它的要求做；拿不准就调用，不要自己猜。别因为「这次很简单」「手边有现成工具」「刚才已经加载过」跳过。**\n"

// entrySkill plants the retired loader where earlier installs put it.
func entrySkill(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "skills", "skillmaster")
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: skillmaster\n---\n")
	return dir
}

// **A preview migrates nothing.** The command says "不加只预览"; the recorded file and any
// existing skill have to look exactly as they did before it ran.
func TestPreviewDoesNotMigrateOrRetire(t *testing.T) {
	dir := isolateConfig(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, "CLAUDE.md")
	writeFile(t, target, releasedBlock)
	loader := entrySkill(t, home)
	if err := rule.SaveState(dir, rule.State{Schema: 1, RuleVersion: 1,
		Files: []rule.File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, status := runCaptured(t, "install", "--file", filepath.Join(home, "other.md")); status != 2 {
		t.Fatalf("status = %d, want 2", status)
	}
	if got, _ := os.ReadFile(target); string(got) != releasedBlock {
		t.Error("a preview migrated the recorded file")
	}
	if _, err := os.Stat(loader); err != nil {
		t.Error("a preview moved the entry skill")
	}
}

// **A machine with no record keeps its old loader**, even when a write-capable command runs.
func TestACommandOnAMachineWithNoRuleKeepsTheEntrySkill(t *testing.T) {
	isolateConfig(t)
	loader := entrySkill(t, os.Getenv("HOME"))
	_, stderr, status := runCaptured(t, "prompt", "print")
	if status != 0 {
		t.Fatalf("prompt print failed: %s", stderr)
	}
	if _, err := os.Stat(loader); err != nil {
		t.Error("the entry skill was renamed on a machine with no rule")
	}
}

// **Nothing renames an entry skill any more.** A recorded file that lost the rule is the case where
// renaming used to be forbidden; now it simply does not happen at all.
func TestACommandNeverTouchesTheEntrySkill(t *testing.T) {
	dir := isolateConfig(t)
	loader := entrySkill(t, os.Getenv("HOME"))
	missing := filepath.Join(os.Getenv("HOME"), "gone.md")
	if err := rule.SaveState(dir, rule.State{Schema: 1, RuleVersion: 1,
		Files: []rule.File{{Path: missing, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "prompt", "print")
	if _, err := os.Stat(loader); err != nil {
		t.Error("a command moved the entry skill")
	}
}

// **Every `install` without `--yes` is read-only, including the ones that will fail.** The preview
// must not migrate other files or touch an existing skill before printing its plan, and a usage error
// must not write first either.
func TestEveryInstallWithoutYesIsReadOnly(t *testing.T) {
	readOnly := [][]string{
		{"install"},
		{"install", "--yes"},
		{"install", "--file", "/tmp/x"},
		{"install", "--file"},
		{"install", "--badflag"},
		{"install", "--check"},
		{"install", "--decline"},
		{"install", "--undo", "--file", "/tmp/x"},
		{"install", "--file", "/tmp/x", "--dry-run"},
		{"install", "--file", "/tmp/x", "--yes", "--dry-run"},
		{"doctor"},
	}
	for _, args := range readOnly {
		if !readOnlyRuleMode(args) {
			t.Errorf("%v is not treated as read-only", args)
		}
	}
	writes := [][]string{{"install", "--file", "/tmp/x", "--yes"}, {"skills", "search", "x"}}
	for _, args := range writes {
		if readOnlyRuleMode(args) {
			t.Errorf("%v is treated as read-only", args)
		}
	}
}

func TestInstallRejectsMixedModes(t *testing.T) {
	isolateConfig(t)
	cases := [][]string{
		{"install", "--check", "--file", "x"},
		{"install", "--undo"},
		{"install", "--file", "x", "--agent", "a", "--decline"},
	}
	for _, args := range cases {
		if _, _, status := runCaptured(t, args...); status != 2 {
			t.Errorf("%v: status = %d, want 2", args, status)
		}
	}
}

func TestDeclineIsRememberedAndCheckSaysSo(t *testing.T) {
	isolateConfig(t)
	stdout, stderr, status := runCaptured(t, "install", "--decline")
	if status != 0 {
		t.Fatalf("decline: %d %s", status, stderr)
	}
	if !strings.Contains(stdout, "不再提示") {
		t.Errorf("decline said %q", stdout)
	}
	stdout, _, status = runCaptured(t, "install", "--check")
	if status != 0 || !strings.Contains(stdout, "已拒绝") {
		t.Errorf("check after decline = %d %q", status, stdout)
	}
}

// **「已安装」 for a file the editor ignores is the silent failure v94 exists to end.** The check
// names the missing frontmatter, gives the remedy that actually fixes it, and exits non-zero so a
// script cannot read the machine as covered.
func TestCheckCallsAFrontmatterlessCursorRuleUnreadable(t *testing.T) {
	isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "skillmaster.mdc")
	if _, stderr, status := runCaptured(t, "install", "--file", target, "--yes"); status != 0 {
		t.Fatalf("install: %d %s", status, stderr)
	}
	stdout, _, status := runCaptured(t, "install", "--check")
	if status != 1 {
		t.Errorf("check status = %d, want 1; output: %s", status, stdout)
	}
	if !strings.Contains(stdout, "读不到") || strings.Contains(stdout, "已安装") {
		t.Errorf("check output = %q; want 读不到 and not 已安装", stdout)
	}
	if !strings.Contains(stdout, "prompt print") {
		t.Errorf("the remedy does not say to fetch the text first: %q", stdout)
	}
}

// **A refusal outlives the files it was made next to.** Someone who installed, then declined, then
// lost the instruction file must not be asked to install again: the check is what the journey reads
// to decide whether to ask a second time.
func TestDeclinedWithRecordsStillSaysDeclined(t *testing.T) {
	isolateConfig(t)
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(os.Getenv("HOME"), "gone.md")
	if err := rule.SaveState(dir, rule.State{Schema: 1, Declined: true,
		Files: []rule.File{{Path: missing, RuleVersion: rule.Version}}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, status := runCaptured(t, "install", "--check")
	if status != 0 || !strings.Contains(stdout, "已拒绝") {
		t.Errorf("check with declined + records = %d %q", status, stdout)
	}
}

func TestInstallingAfterDecliningTurnsMigrationBackOn(t *testing.T) {
	isolateConfig(t)
	if _, stderr, status := runCaptured(t, "install", "--decline"); status != 0 {
		t.Fatalf("decline: %s", stderr)
	}
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	if _, stderr, status := runCaptured(t, "install", "--file", target, "--yes"); status != 0 {
		t.Fatalf("install: %s", stderr)
	}
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := rule.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Declined {
		t.Error("a successful install left the machine marked as declined")
	}
}
