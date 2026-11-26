package rule

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestInstallCreatesAFileWithExactlyOneCopy(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	res, err := Install(dir, target, "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "created" {
		t.Errorf("action = %q, want created", res.Action)
	}
	blob := readFile(t, target)
	if !bytes.Equal(blob, Block()) {
		t.Errorf("created file is not exactly the block:\n%s", blob)
	}
	if bytes.Count(blob, []byte("SkillMaster 守则：")) != 1 {
		t.Errorf("the rule appears %d times, want 1", bytes.Count(blob, []byte("SkillMaster 守则：")))
	}
}

func TestInstallAppendsAfterANewlineAndKeepsTheRest(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	original := []byte("# 我的指令\n\n- 先说结论\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, target, "codex", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "appended" {
		t.Errorf("action = %q, want appended", res.Action)
	}
	blob := readFile(t, target)
	if !bytes.HasPrefix(blob, original) {
		t.Error("the original bytes changed")
	}
	if !bytes.Contains(blob, []byte("\n\nSkillMaster 守则：")) {
		t.Errorf("the rule is not separated by one blank line:\n%s", blob)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if _, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, target)
	res, err := Install(dir, target, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", res.Action)
	}
	if !bytes.Equal(before, readFile(t, target)) {
		t.Error("the second run changed the file")
	}
}

func TestInstallReplacesOnlyTheExactLegacyBlock(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	legacy := legacyBlocks[0]
	if err := os.WriteFile(target, append([]byte("别的规则\n"), legacy...), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, target, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "replaced" {
		t.Errorf("action = %q, want replaced", res.Action)
	}
	blob := readFile(t, target)
	if bytes.Contains(blob, legacy) {
		t.Error("the legacy block is still there")
	}
	if !bytes.HasPrefix(blob, []byte("别的规则\n")) {
		t.Error("the surrounding text changed")
	}
}

func TestUndoRestoresTheByteIdenticalFile(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	original := []byte("原有内容\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(dir, target); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, readFile(t, target)) {
		t.Errorf("undo did not restore the original:\n%s", readFile(t, target))
	}
}

func TestCheckReportsPresentAbsentAndModified(t *testing.T) {
	dir := tempDir(t)
	present := filepath.Join(dir, "a.md")
	modified := filepath.Join(dir, "b.md")
	if _, err := Install(dir, present, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, modified, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modified, []byte("改掉了\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, st := range statuses {
		got[st.File.Path] = st.State
	}
	if got[present] != "present" || got[modified] != "modified" {
		t.Errorf("statuses = %v, want present/modified", got)
	}
}

func TestMigrateRewritesTheExactBlockAndLeavesAnEditedOne(t *testing.T) {
	dir := tempDir(t)
	managed := filepath.Join(dir, "managed.md")
	edited := filepath.Join(dir, "edited.md")
	if err := os.WriteFile(managed, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	// **Edited inside the block, not appended after it.** A block that is still there byte for byte
	// with a sentence added below is the same old rule; a changed sentence is a decision.
	editedBlock := bytes.Replace(legacyBlocks[0], []byte("铁律"), []byte("规则"), 1)
	if err := os.WriteFile(edited, editedBlock, 0o644); err != nil {
		t.Fatal(err)
	}
	st := State{Schema: 1, RuleVersion: 1, Files: []File{
		{Path: managed, RuleVersion: 1},
		{Path: edited, RuleVersion: 1},
	}}
	if err := SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
	n, err := MigrateIfNeeded(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("migrated %d files, want 1", n)
	}
	if !bytes.Equal(readFile(t, managed), Block()) {
		t.Error("the recorded file was not migrated")
	}
	if !bytes.Equal(readFile(t, edited), editedBlock) {
		t.Error("a file people edited was rewritten")
	}
}

// v25Block is the exact text `prompt install` wrote into a person's file.
//
// **Written out here rather than read from `legacyBlocks`.** The seeding path exists only for
// machines whose `prompt.json` came from v25/v26, and `prompt.json` can only point at a file this
// text was written into. A test that seeded `legacyBlocks[0]` (the 1.6–1.8 line) passed while the
// real case failed, because the catalog did not contain this block at all.
const v25Block = "<!-- skillmaster:begin -->\n" +
	"SkillMaster 管我的全部 skill：查找、调用、编辑、整理，都通过 skillmaster CLI 做。\n" +
	"要按某个外部系统的规矩做事、要产出有固定格式的东西、你正要凭记忆写多步流程，或用户让你按我们的做法做时，先用它；拿不准就查一次。\n" +
	"先 skillmaster skills search \"<一句话说明要做的事>\"；结果的第二行写着每个 skill 什么时候适用。没命中换种说法，仍没有加 --scope public；用 skill invoke <地址> 加载；都没有就直说没找到；更多命令见 skillmaster help。\n" +
	"<!-- skillmaster:end -->\n"

// **A v25 machine's record is still a managed file.** The retired `prompt install` wrote
// `prompt.json` with explicit paths; migration seeds `install.json` from it so an old user's exact
// block is replaced without running a command.
func TestMigrateSeedsFromTheRetiredPromptStore(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, []byte(v25Block), 0o644); err != nil {
		t.Fatal(err)
	}
	store := `{"schema":2,"files":[{"path":"` + target + `","agent":"claude","digest":"sha256:x"}]}`
	if err := os.WriteFile(filepath.Join(dir, "prompt.json"), []byte(store), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 1 {
		t.Fatalf("migrated %d, err %v; want 1, nil", n, err)
	}
	if !bytes.Equal(readFile(t, target), Block()) {
		t.Error("the seeded file was not migrated")
	}
	// **The migration's backup must be recorded**, or the documented `--undo` truncates the file.
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := st.Find(target)
	if !ok || rec.Backup == "" {
		t.Fatalf("the migrated record has no backup: %+v", rec)
	}
	if _, err := Undo(dir, target); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, target), []byte(v25Block)) {
		t.Error("undo after a seeded migration did not restore the original")
	}
}

// **Every form the catalog lists must actually be replaced**, including on a CRLF machine.
func TestMigrateUnderstandsEveryHistoricalForm(t *testing.T) {
	for i, old := range legacyBlocks {
		for _, text := range lineEndingVariants(old) {
			dir := tempDir(t)
			target := filepath.Join(dir, "CLAUDE.md")
			if err := os.WriteFile(target, text, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
				t.Fatal(err)
			}
			if n, err := MigrateIfNeeded(dir); err != nil || n != 1 {
				t.Fatalf("block %d: migrated %d, err %v; want 1, nil", i, n, err)
			}
			if !containsBlock(readFile(t, target)) {
				t.Errorf("block %d was not replaced", i)
			}
		}
	}
}

// **A seeded label must be something the server accepts.** `prompt install --agent` was never
// validated, so `Claude Code` is a real value in old `prompt.json` files.
func TestMigrateNormalizesASeededAgentLabel(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	store := `{"schema":2,"files":[{"path":"` + target + `","agent":"Claude Code"}]}`
	if err := os.WriteFile(filepath.Join(dir, "prompt.json"), []byte(store), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIfNeeded(dir); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 1 || st.Files[0].Agent != "claude-code" {
		t.Fatalf("seeded agent = %+v, want claude-code", st.Files)
	}
}

// **A deleted record must not hold the whole state back.** Its file cannot be migrated, but it can
// also never succeed; retrying it on every command forever would keep the top-level version — the
// number the report sends — at the old value.
func TestMigrateLetsADeletedFileSettle(t *testing.T) {
	dir := tempDir(t)
	gone := filepath.Join(dir, "gone.md")
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: gone, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIfNeeded(dir); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.RuleVersion != Version {
		t.Errorf("top-level version = %d, want %d", st.RuleVersion, Version)
	}
}

// **An `install` of one file must not mark the others migrated.** That would stop a later build
// from retrying a file whose migration had failed (permission, a locked editor).
func TestInstallDoesNotAdvancePastAnUnmigratedFile(t *testing.T) {
	dir := tempDir(t)
	old := filepath.Join(dir, "old.md")
	if err := os.WriteFile(old, []byte("person's own text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: old, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, filepath.Join(dir, "new.md"), "codex", false); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.RuleVersion >= Version {
		t.Errorf("top-level version = %d after installing one file; the other is still at 1", st.RuleVersion)
	}
}

// **An explicit install undoes a refusal even when the rule is already there.** Someone who
// declined, then pasted the rule by hand and registered it, is not refusing any more — and a stale
// `declined` would also silence every later migration.
func TestInstallClearsDeclinedWhenTheRuleIsAlreadyThere(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, Declined: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "claude-code", false); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Declined {
		t.Error("declined is still set after an explicit install")
	}
	if st.Files[0].Agent != "claude-code" {
		t.Errorf("agent = %q, want claude-code", st.Files[0].Agent)
	}
}

// **`--agent` can be corrected on a file that is already up to date**, otherwise the label a person
// typed wrong is permanent.
func TestInstallUpdatesTheAgentLabelOfAnUnchangedFile(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if _, err := Install(dir, target, "codex", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "Claude Code", false); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files[0].Agent != "claude-code" {
		t.Errorf("agent = %q, want claude-code", st.Files[0].Agent)
	}
}

// **The text every released 1.6–1.8 wrote must migrate.** The first entry is that exact text; a
// catalog containing only the never-released v93 wording would leave almost every old machine alone.
func TestMigrateUnderstandsTheReleasedOneLineBlock(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	released := legacyBlocks[0]
	if !bytes.Contains(released, []byte("SkillMaster 这个 skill")) {
		t.Fatalf("the first legacy block is not the released text: %s", released)
	}
	if err := os.WriteFile(target, released, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 1 {
		t.Fatalf("migrated %d, err %v", n, err)
	}
	if !bytes.Equal(readFile(t, target), Block()) {
		t.Error("the released one-line block was not migrated")
	}
}

// **A hand-written rule is not a second copy to append.** Once the heading is there but the text
// differs, the file is the person's; the command refuses instead of leaving two rule sets.
func TestInstallRefusesToAppendBesideAnEditedRule(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	edited := bytes.Replace(Block(), []byte("闲聊、常识问答和简单计算/翻译不用。"), []byte("闲聊也不用。"), 1)
	if err := os.WriteFile(target, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "", false); err == nil {
		t.Fatal("an edited rule was accepted")
	}
	if !bytes.Equal(readFile(t, target), edited) {
		t.Error("the refused install changed the file")
	}
}

// **A file the person pasted into by hand still gets a record.** `prompt print` → paste →
// `install --check` is the documented fallback; without this the check says "no record" forever.
func TestInstallRecordsAFileThatAlreadyHasTheRule(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "Codex CLI", false); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(dir)
	if err != nil || len(statuses) != 1 || statuses[0].State != "present" {
		t.Fatalf("check = %+v, %v", statuses, err)
	}
	if statuses[0].File.Agent != "codex-cli" {
		t.Errorf("agent label = %q, want codex-cli", statuses[0].File.Agent)
	}
}

func TestUndoRefusesToTruncateAFileItDidNotCreate(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, []byte("原有内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, Files: []File{{Path: target, Agent: "claude-code"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(dir, target); err == nil {
		t.Fatal("undo truncated a pre-existing file without a backup")
	}
	if string(readFile(t, target)) != "原有内容\n" {
		t.Error("the refused undo changed the file")
	}
}

// **A repository under the home directory is still a project.** Most projects live there, so an
// "under home is fine" shortcut would allow exactly the file the promise forbids. `.git` is a file
// in a linked worktree, which the directory-only check missed.
func TestInstallRefusesAProjectFileUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "Developer", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(t.TempDir(), filepath.Join(repo, "CLAUDE.md"), "claude-code", false); err == nil {
		t.Fatal("a project file under home was accepted")
	}
	// **Home itself is allowed**: a dotfiles repository at `~/` still versions the person's own
	// instruction files, which is what this command writes.
	dotfiles := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dotfiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(t.TempDir(), filepath.Join(dotfiles, "CLAUDE.md"), "claude-code", false); err != nil {
		t.Fatalf("a dotfiles repository at home was refused: %v", err)
	}
}

// **Codex reads `CODEX_HOME` and prefers `AGENTS.override.md`.** Detection that only looked at
// `~/.codex/AGENTS.md` would report those machines as absent — or as present when Codex never reads
// that file.
func TestDefaultPresentFollowsCodexHomeAndOverride(t *testing.T) {
	home := t.TempDir()
	codex := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	if err := os.WriteFile(filepath.Join(codex, "AGENTS.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); !present || !known {
		t.Fatal("a rule in $CODEX_HOME/AGENTS.md was not detected")
	}
	// An override without the rule wins: Codex reads only that file, so the answer is "absent".
	if err := os.WriteFile(filepath.Join(codex, "AGENTS.override.md"), []byte("别的内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, _ := DefaultPresent(home); present {
		t.Error("AGENTS.md was counted although AGENTS.override.md exists and has no rule")
	}
}

// **WorkBuddy reads a per-user file, not the one at the root of its directory.** `userDir` is
// `~/.workbuddy/user-<uid>-personal` once a user is signed in; `~/.workbuddy/MEMORY.md` only counts
// before that. Detecting just the root file reported an installed rule as absent (real device,
// 2026-11-26).
func TestDefaultPresentFindsWorkBuddysPerUserFile(t *testing.T) {
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".workbuddy")
	userDir := filepath.Join(root, "user-abc-personal")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Nothing anywhere yet: "no file to look at" must stay distinguishable from "rule absent".
	if present, known := DefaultPresent(home); present || known {
		t.Fatalf("empty home reported present=%v known=%v", present, known)
	}
	// A different user's file without the rule is evidence, but not the rule.
	other := filepath.Join(root, "user-xyz-personal")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "MEMORY.md"), []byte("别的内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); present || !known {
		t.Fatalf("a user file without the rule reported present=%v known=%v; want false, true", present, known)
	}
	if err := os.WriteFile(filepath.Join(userDir, "MEMORY.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); !present || !known {
		t.Fatal("a rule in ~/.workbuddy/user-<id>-personal/MEMORY.md was not detected")
	}
	// The root file is what the app reads before login, so it stays a valid place for the rule.
	unbound := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unbound, ".workbuddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unbound, ".workbuddy", "MEMORY.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, _ := DefaultPresent(unbound); !present {
		t.Fatal("the unbound root file was not detected")
	}
}

// **A migrated file's record must reach disk even when a sibling cannot be migrated.** Otherwise
// every later command rewrites the same file and leaves another backup beside it.
func TestMigrateSavesTheRecordBesideAnUnmigratedFile(t *testing.T) {
	dir := tempDir(t)
	managed := filepath.Join(dir, "managed.md")
	edited := filepath.Join(dir, "edited.md")
	if err := os.WriteFile(managed, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	editedBlock := bytes.Replace(legacyBlocks[0], []byte("铁律"), []byte("规则"), 1)
	if err := os.WriteFile(edited, editedBlock, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{
		{Path: managed, RuleVersion: 1},
		{Path: edited, RuleVersion: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 1 {
		t.Fatalf("first run migrated %d, err %v; want 1, nil", n, err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec, _ := st.Find(managed); rec.RuleVersion != Version {
		t.Errorf("the migrated record stayed at %d; it will be migrated again", rec.RuleVersion)
	}
	// The second run must not touch the managed file again.
	before := readFile(t, managed)
	if n, err := MigrateIfNeeded(dir); err != nil || n != 0 {
		t.Fatalf("second run migrated %d, err %v; want 0, nil", n, err)
	}
	if !bytes.Equal(readFile(t, managed), before) {
		t.Error("the second run rewrote a file that was already migrated")
	}
}

// **A record this command may never write settles instead of pinning the state forever.** A v26
// `prompt install` could point into a repository; without this the top-level version — the number
// the report sends — stays old on every future run.
func TestMigrateSettlesAProjectRecordAsUnmanaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "Developer", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "CLAUDE.md")
	if err := os.WriteFile(target, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	dir := tempDir(t)
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIfNeeded(dir); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Files[0].Unmanaged {
		t.Error("the project record is not marked unmanaged")
	}
	if st.RuleVersion != Version {
		t.Errorf("top-level version = %d, want %d", st.RuleVersion, Version)
	}
	if !bytes.Equal(readFile(t, target), legacyBlocks[0]) {
		t.Error("the project file was rewritten")
	}
	// `--undo` drops the record without touching the repository file.
	if _, err := Undo(dir, target); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := LoadState(dir); len(st.Files) != 0 {
		t.Errorf("the unmanaged record was not removed: %+v", st.Files)
	}
	if !bytes.Equal(readFile(t, target), legacyBlocks[0]) {
		t.Error("removing the record changed the file")
	}
}

// **An exact old block is not an edit.** A read-only check must say what will happen instead of
// reporting the person's untouched file as modified.
func TestCheckReportsAnExactOldBlockAsLegacy(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "legacy" {
		t.Fatalf("status = %+v, want legacy", statuses)
	}
}

// **A current rule beside a leftover old block is still two rule sets.** That is what the hand-paste
// fallback produces (paste the new text into the file that has the old one), and the old block has
// to go without touching the new one.
func TestInstallRemovesALeftoverOldBlockBesideTheCurrentRule(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	content := string(Block()) + "\n" + string(legacyBlocks[0])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, target, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "cleaned" {
		t.Errorf("action = %q, want cleaned", res.Action)
	}
	blob := readFile(t, target)
	if bytes.Count(blob, []byte("SkillMaster 守则：")) != 1 {
		t.Errorf("the rule appears %d times, want 1:\n%s", bytes.Count(blob, []byte("SkillMaster 守则：")), blob)
	}
	if bytes.Contains(blob, legacyBlocks[0]) {
		t.Error("the old block is still there")
	}
}

// **A sandbox moves writes, not reads.** When the writable state directory is a fallback holding no
// records, the machine's real install.json is still readable — and `--check`, `doctor` and the
// coverage report all depend on it (measured with sandbox-exec, 2026-11-26).
func TestLoadStateReadsTheRealFileWhenTheWritableDirectoryIsEmpty(t *testing.T) {
	saved := stateReadPath
	t.Cleanup(func() { stateReadPath = saved })

	real, fallback := t.TempDir(), t.TempDir()
	record := File{Path: filepath.Join(real, "CLAUDE.md"), Agent: "claude-code", RuleVersion: 2}
	if err := SaveState(real, State{Schema: 1, RuleVersion: 2, Files: []File{record}}); err != nil {
		t.Fatal(err)
	}
	SetStateReadPath(StatePath(real))

	st, ok, err := LoadState(fallback)
	if err != nil || !ok || len(st.Files) != 1 {
		t.Fatalf("LoadState = %+v, ok=%v, err=%v; want the real record", st, ok, err)
	}
	// A save stays in the writable directory: the real file is not rewritten from a sandbox.
	if err := SaveState(fallback, State{Schema: 1, RuleVersion: 3, Files: []File{record}}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := LoadState(real); got.RuleVersion != 2 {
		t.Fatalf("the real state was rewritten from the fallback: %+v", got)
	}
	// Once the writable directory has a file of its own, that one wins.
	if got, ok, _ := LoadState(fallback); !ok || got.RuleVersion != 3 {
		t.Fatalf("the directory's own file did not win: %+v ok=%v", got, ok)
	}
}

// **A newer rule can extend an older one.** When the current block contains a legacy text as a
// substring, the leftover cleanup has to leave it alone: otherwise the "old" block is cut out of
// the current rule and the file keeps only the appended tail (real update, 2026-11-26).
func TestCleaningDoesNotCutTheCurrentRuleOutOfItself(t *testing.T) {
	firstLine := Block()[:bytes.IndexByte(Block(), '\n')]
	saved := legacyBlocks
	legacyBlocks = append([][]byte{firstLine}, saved...)
	t.Cleanup(func() { legacyBlocks = saved })

	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, target, "codex", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", res.Action)
	}
	if blob := readFile(t, target); !bytes.Equal(blob, Block()) {
		t.Fatalf("the rule was cut down to:\n%s", blob)
	}
}

// The same guard on the migration path: a record one version behind must not have its rule cut.
func TestMigrationDoesNotCutTheCurrentRuleOutOfItself(t *testing.T) {
	firstLine := Block()[:bytes.IndexByte(Block(), '\n')]
	saved := legacyBlocks
	legacyBlocks = append([][]byte{firstLine}, saved...)
	t.Cleanup(func() { legacyBlocks = saved })

	dir := tempDir(t)
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{
		{Path: target, RuleVersion: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIfNeeded(dir); err != nil {
		t.Fatal(err)
	}
	if blob := readFile(t, target); !bytes.Equal(blob, Block()) {
		t.Fatalf("migration cut the rule down to:\n%s", blob)
	}
}

// **A deleted file is 「不见了」, even when its old path was inside a repository.** The guards must
// not turn a missing file into an unmanaged one.
func TestMigrateTreatsADeletedProjectRecordAsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "Developer", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(repo, "CLAUDE.md")
	dir := tempDir(t)
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: gone, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIfNeeded(dir); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "absent" {
		t.Fatalf("status = %+v, want absent", statuses)
	}
}

// **An explicit install takes an unmanaged record back**, and that change has to reach disk.
func TestInstallTakesBackAnUnmanagedRecord(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: Version, Files: []File{{
		Path: target, Agent: "codex", RuleVersion: Version, Digest: Digest(), Unmanaged: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files[0].Unmanaged {
		t.Error("the record is still unmanaged after an explicit install")
	}
	if st.Files[0].Agent != "codex" {
		t.Errorf("agent = %q, want the recorded codex", st.Files[0].Agent)
	}
}

// **A write without `--agent` keeps the label already recorded.**
func TestInstallWriteKeepsTheRecordedAgentWithoutTheFlag(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if _, err := Install(dir, target, "claude-code", false); err != nil {
		t.Fatal(err)
	}
	// Make the file carry a legacy block again, so the next install takes the writing path.
	if err := os.WriteFile(target, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, target, "", false); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files[0].Agent != "claude-code" {
		t.Errorf("agent = %q, want claude-code", st.Files[0].Agent)
	}
}

// **`--agent ""` is a person clearing the label**, not the same thing as leaving the flag out.
func TestInstallWithAnEmptyAgentClearsTheLabel(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	if _, err := Install(dir, target, "claude-code", false); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if _, err := InstallWithAgent(dir, target, &empty, false); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files[0].Agent != "unknown" {
		t.Errorf("agent = %q, want unknown", st.Files[0].Agent)
	}
}

// **A recorded path that turned into a link is out of scope**, not 「已安装」 through the target.
func TestCheckTreatsALinkedRecordAsUnmanaged(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	linked := filepath.Join(dir, "linked.md")
	if err := os.WriteFile(target, Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: Version, Files: []File{
		{Path: linked, RuleVersion: Version},
	}}); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "unmanaged" {
		t.Fatalf("status = %+v, want unmanaged", statuses)
	}
	if FileHasRule(linked) {
		t.Error("coverage counted a link")
	}
}

// **Cleaning up a leftover old block backs the file up**, like every other write.
func TestMigrateCleaningBacksTheFileUp(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "CLAUDE.md")
	content := string(Block()) + "\n" + string(legacyBlocks[0])
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(dir, State{Schema: 1, RuleVersion: 1, Files: []File{{Path: target, RuleVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 1 {
		t.Fatalf("migrated %d, err %v; want 1, nil", n, err)
	}
	st, _, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files[0].Backup == "" {
		t.Error("the cleaned file has no backup")
	}
	if bytes.Contains(readFile(t, target), legacyBlocks[0]) {
		t.Error("the old block survived")
	}
}

func TestNormalizeAgentMakesTheServerRegex(t *testing.T) {
	cases := map[string]string{
		"Claude Code":  "claude-code",
		"  WorkBuddy ": "workbuddy",
		"Codex CLI":    "codex-cli",
		"":             "unknown",
		"中文":           "unknown",
	}
	for in, want := range cases {
		if got := NormalizeAgent(in); got != want {
			t.Errorf("NormalizeAgent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMigrateDoesNothingWhenDeclined(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "managed.md")
	if err := os.WriteFile(target, legacyBlocks[0], 0o644); err != nil {
		t.Fatal(err)
	}
	st := State{Schema: 1, RuleVersion: 1, Declined: true, Files: []File{{Path: target, RuleVersion: 1}}}
	if err := SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
	if n, err := MigrateIfNeeded(dir); err != nil || n != 0 {
		t.Fatalf("migrated %d, err %v; want 0, nil", n, err)
	}
	if !bytes.Contains(readFile(t, target), legacyBlocks[0]) {
		t.Error("a declined machine was migrated")
	}
}

// **A person's existing skill is theirs.** v94 stopped installing the entry skill, and it also
// stopped renaming one it finds: leaving the old loader where it is was the deliberate call
// (2026-11-26). Installing the rule must not move or rewrite it.
func TestInstallLeavesAnExistingEntrySkillAlone(t *testing.T) {
	home := tempDir(t)
	dir := filepath.Join(home, ".claude", "skills", "skillmaster")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skill, []byte("---\nname: skillmaster\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := tempDir(t)
	if _, err := Install(stateDir, filepath.Join(home, ".claude", "CLAUDE.md"), "claude-code", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("the existing entry skill is gone: %v", err)
	}
	if _, err := os.Stat(dir + ".disabled"); err == nil {
		t.Error("the skill directory was renamed")
	}
}

func TestDefaultPresentReadsOnlyKnownFiles(t *testing.T) {
	home := tempDir(t)
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); present || known {
		t.Fatalf("empty home: present=%v known=%v", present, known)
	}
	if err := os.WriteFile(filepath.Join(claude, "CLAUDE.md"), Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); !present || !known {
		t.Fatalf("installed home: present=%v known=%v", present, known)
	}
}
