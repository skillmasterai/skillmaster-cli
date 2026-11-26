package rule

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/skillmasterai/skillmaster-cli/internal/prompt"
)

// Result says what one install did, or would do in a dry run.
type Result struct {
	Path    string
	Action  string // created / appended / replaced / unchanged
	DryRun  bool
	Plan    string
	Created bool
	// Notice is a warning the command prints after the plan. Today the one case is a Cursor `.mdc`
	// rule file without frontmatter: Cursor silently ignores such a file, so the write "succeeds"
	// and changes nothing (found on a real install, 2026-11-26).
	Notice string
}

// ExpandPath turns a `--file` value into an absolute path.
//
// **Only `~` is expanded, and only at the front.** The value is what the person typed; a shell that
// already expanded it hands over the absolute form, and anything else is a path, not a template.
func ExpandPath(path string) (string, error) {
	// **Backslash after `~` on Windows only.** A path typed there, or by an agent quoting one, is
	// `~\.codex\AGENTS.md`; `filepath.Abs` would treat the `~` as a directory name and create it.
	// On Unix a backslash is a legal file-name character, and `~\x` is not the home directory.
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `~\`) {
		path = "~/" + strings.TrimPrefix(path, `~\`)
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("找不到用户主目录：%w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// Install writes the current rule into path, or reports that it is already there.
//
// **The file is the person's**: the rule is appended as a paragraph, or replaces the exact legacy
// block an earlier version wrote. Nothing else in the file is touched.
func Install(dir, path, agent string, dryRun bool) (Result, error) {
	var label *string
	if strings.TrimSpace(agent) != "" {
		label = &agent
	}
	return InstallWithAgent(dir, path, label, dryRun)
}

// InstallWithAgent is Install where "no --agent" and "--agent """ are different answers: a nil
// label keeps the label already recorded, an empty one is the person clearing it to `unknown`.
func InstallWithAgent(dir, path string, agent *string, dryRun bool) (Result, error) {
	abs, err := ExpandPath(path)
	if err != nil {
		return Result{}, err
	}
	if err := refuseProjectLevel(abs); err != nil {
		return Result{}, err
	}
	if err := refuseSymlink(abs); err != nil {
		return Result{}, err
	}
	st, _, err := LoadState(dir)
	if err != nil {
		return Result{}, err
	}
	content, existed, mode, err := readTarget(abs)
	if err != nil {
		return Result{}, err
	}
	merged, action := merge(content, Block())
	res := Result{Path: abs, Action: action, DryRun: dryRun, Plan: planFor(abs, action), Created: !existed}
	if action != "conflict" {
		res.Notice = mdcNotice(abs, merged)
	}
	if action == "conflict" {
		return Result{}, fmt.Errorf("%s 里已经有一段被改过的守则；先自己备份并清掉那段，再重装。", abs)
	}
	if action == "unchanged" {
		if dryRun {
			return res, nil
		}
		// **A file the person pasted the rule into by hand still becomes a record.** Without this,
		// the documented fallback (`prompt print` → paste → `install --file` → `install --check`)
		// could never pass: check reads records, and nothing would ever write one.
		rec, known := st.Find(abs)
		changed := false
		if !known {
			rec = File{Path: abs, RuleVersion: Version, Digest: Digest()}
			changed = true
		}
		// `--agent` is a label the person can correct; an install without it keeps the old one.
		if agent != nil {
			if label := NormalizeAgent(*agent); rec.Agent != label {
				rec.Agent = label
				changed = true
			}
		}
		// The file already carries the current rule, so the record says so — including one seeded
		// at version 0 that the person has since pasted the new text into by hand.
		if rec.Unmanaged {
			rec.Unmanaged = false
			changed = true
		}
		if rec.RuleVersion != Version || rec.Digest != Digest() {
			rec.RuleVersion, rec.Digest = Version, Digest()
			changed = true
		}
		if changed {
			if rec.WrittenAt == "" {
				rec.WrittenAt = time.Now().UTC().Format(time.RFC3339)
			}
			st.Put(rec)
		}
		// **Explicitly installing again undoes a refusal**, whichever branch we are in: keeping
		// `declined` would silently skip every later migration and label the machine as refusing.
		if st.Declined {
			st.Declined = false
			changed = true
		}
		if changed {
			syncRuleVersion(&st)
			if err := SaveState(dir, st); err != nil {
				return Result{}, err
			}
		}
		return res, nil
	}
	if dryRun {
		return res, nil
	}
	backup, err := backupIfNeeded(abs, content, existed)
	if err != nil {
		return Result{}, err
	}
	if err := writeTarget(abs, merged, mode); err != nil {
		return Result{}, err
	}
	// **Read back what is actually on disk**, not what was meant to be written: this digest is what
	// later detection trusts, and a write that silently failed must not be recorded as a success.
	got, err := os.ReadFile(abs)
	if err != nil || !containsBlock(got) {
		return Result{}, fmt.Errorf("写入 %s 后回读不到守则，没有记为已安装", abs)
	}
	label := "unknown"
	if agent != nil {
		label = NormalizeAgent(*agent)
	} else if prev, ok := st.Find(abs); ok && prev.Agent != "" {
		// **An install without `--agent` keeps the label already recorded.** The write changes the
		// rule, not who reads the file.
		label = prev.Agent
	}
	created := !existed
	if prev, ok := st.Find(abs); ok && prev.Created {
		created = true
	}
	rec := File{
		Path:        abs,
		Agent:       label,
		RuleVersion: Version,
		Digest:      Digest(),
		WrittenAt:   time.Now().UTC().Format(time.RFC3339),
		Created:     created,
	}
	if prev, ok := st.Find(abs); ok && prev.Backup != "" {
		rec.Backup = prev.Backup
	} else {
		rec.Backup = backup
	}
	st.Put(rec)
	// **The top-level version is a summary, not a claim.** It moves only when every record is at
	// the current version; otherwise a partial migration would be marked done by any later
	// `install` and the files that failed would never be retried.
	syncRuleVersion(&st)
	// **Installing again undoes a refusal.** The person asked for the rule explicitly; keeping
	// `declined` would silently skip every later migration and label them as refusing forever.
	st.Declined = false
	if err := SaveState(dir, st); err != nil {
		return Result{}, err
	}
	return res, nil
}

// Undo restores the backup, or truncates a file this command created.
//
// **No delete** (ADR 0101): the file stays, empty if this CLI created it. A file that existed before
// and has no backup is not touched: truncating it would destroy content this command did not write.
func Undo(dir, path string) (Result, error) {
	abs, err := ExpandPath(path)
	if err != nil {
		return Result{}, err
	}
	st, _, err := LoadState(dir)
	if err != nil {
		return Result{}, err
	}
	rec, ok := st.Find(abs)
	if !ok {
		return Result{}, fmt.Errorf("%s 不在安装记录里，没有可撤销的内容。", abs)
	}
	// **An unmanaged record is dropped, not written.** Its file is outside what this command is
	// allowed to touch (a repository, a symlink, a non-regular file); removing the record is the
	// only way to let the person take the path out of the state.
	// **The same test `Check` shows as 「不管」 has to be the one `Undo` honours.** A record that
	// settled before its path became a symlink is not marked Unmanaged until the next rule version,
	// so trusting only that flag made the documented remedy (`install --file … --undo`) fail.
	if rec.Unmanaged || !PathIsManageable(abs) {
		st.Remove(abs)
		if err := SaveState(dir, st); err != nil {
			return Result{}, err
		}
		return Result{Path: abs, Action: "dropped", Plan: "已从安装记录里移除（文件未改动）：" + abs}, nil
	}
	// **A file that is already gone needs no restore.** Removing the record is the whole undo; the
	// alternative — recreating an empty file the person (or their agent) deleted — writes where it
	// was not asked to.
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		st.Remove(abs)
		if err := SaveState(dir, st); err != nil {
			return Result{}, err
		}
		return Result{Path: abs, Action: "dropped", Plan: "文件已不在，只移除了安装记录：" + abs}, nil
	}
	if err := refuseProjectLevel(abs); err != nil {
		return Result{}, err
	}
	if err := refuseSymlink(abs); err != nil {
		return Result{}, err
	}
	_, _, mode, _ := readTarget(abs)
	switch {
	case rec.Backup != "":
		blob, err := os.ReadFile(rec.Backup)
		if err != nil {
			return Result{}, fmt.Errorf("备份 %s 读不出来：%w", rec.Backup, err)
		}
		if err := writeTarget(abs, blob, mode); err != nil {
			return Result{}, err
		}
	case rec.Created:
		if err := writeTarget(abs, nil, mode); err != nil {
			return Result{}, err
		}
	default:
		return Result{}, fmt.Errorf("%s 在安装前就存在，也没有可用的备份；不撤销，避免清空你自己的内容。", abs)
	}
	st.Remove(abs)
	if err := SaveState(dir, st); err != nil {
		return Result{}, err
	}
	return Result{Path: abs, Action: "restored", Plan: "已撤销：" + abs}, nil
}

// FileHasRule reports whether path still carries the current rule. A missing file is false, and so
// is a path that is no longer a regular file: coverage must not be counted through a link.
//
// **A Cursor rule file the editor ignores is not coverage.** `.mdc` without frontmatter is loaded by
// nothing, so the block being there would report an installed rule the agent never sees.
func FileHasRule(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	blob, err := os.ReadFile(path)
	if err != nil || !containsBlock(blob) {
		return false
	}
	return mdcNotice(path, blob) == ""
}

// PathIsManageable reports whether path is a file this command may write to on request: a missing
// path counts (an absent record is still a record), a regular file counts, and anything else — a
// symlink, a directory, a socket — does not.
//
// **`Check`, `Undo` and the coverage report must answer this the same way.** One of them deciding a
// record is 「不管」 while another writes through it, or counts it as coverage, is how a state file
// and a disk end up disagreeing.
func PathIsManageable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return true
	}
	return info.Mode().IsRegular()
}

// Status is one recorded file's state, as `install --check` reports it.
type Status struct {
	File   File
	State  string // present / legacy / ignored / absent / modified / unmanaged
	Detail string
}

// Check reads every recorded file and reports whether the rule is still there.
func Check(dir string) ([]Status, error) {
	st, _, err := LoadState(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(st.Files))
	for _, f := range st.Files {
		if f.Unmanaged {
			out = append(out, Status{File: f, State: "unmanaged"})
			continue
		}
		// **A path that became a link is not a file this command manages.** Reading through it would
		// report 「已安装」 for a rule that lives in the link's target — perhaps inside a repository.
		if !PathIsManageable(f.Path) {
			out = append(out, Status{File: f, State: "unmanaged", Detail: "路径现在是符号链接或不是普通文件"})
			continue
		}
		blob, err := os.ReadFile(f.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out = append(out, Status{File: f, State: "absent", Detail: "文件不存在"})
		case err != nil:
			out = append(out, Status{File: f, State: "absent", Detail: err.Error()})
		case (containsBlock(blob) || legacyBlockIndex(blob) != nil) && mdcNotice(f.Path, blob) != "":
			// **The bytes are there and the editor reads none of them.** Calling this 「已安装」 (or
			// 「旧版」, which promises the next write fixes it — it does not, the frontmatter is what
			// is missing) is the silent failure v94 exists to end: Cursor loads no rule at all.
			out = append(out, Status{File: f, State: "ignored", Detail: "文件里有守则，但缺 frontmatter；Cursor 会忽略整个文件"})
		case containsBlock(blob):
			// **Two rule sets is not 「已安装」 in the state the person asked about.** The current
			// rule is there; an exact old copy beside it is what a hand-paste leaves, and only a
			// write-capable command removes it.
			detail := ""
			if _, _, leftover := legacySpanOutside(blob, Block()); leftover {
				detail = "旁边还有一段旧守则；skmr install --file <同一路径> --yes 会清掉"
			}
			out = append(out, Status{File: f, State: "present", Detail: detail})
		case headingOutsideLegacy(blob):
			// **A heading that is not an exact old block is the person's edit** — including when an
			// exact old copy sits beside it. Saying 「旧版；下一次会替换」 would promise a write that
			// the conflict guard refuses, so this answers first.
			out = append(out, Status{File: f, State: "modified", Detail: "守则被改过，旁边可能还有一段旧守则；install 不会替你改"})
		case legacyBlockIndex(blob) != nil:
			// **An exact old block is not an edit.** Only a command that may write replaces it, so a
			// read-only check has to say what will happen rather than call the person's file altered.
			out = append(out, Status{File: f, State: "legacy", Detail: "旧版守则；下一次会写文件的命令会替换"})
		default:
			out = append(out, Status{File: f, State: "modified", Detail: "守则被改过或删掉了"})
		}
	}
	return out, nil
}

// MigrateIfNeeded replaces the exact legacy block in every recorded file when the rule version moved.
//
// **Silent and best effort** (ADR 0242): it runs on any command that may write, touches only files
// this machine recorded, and never changes a command's exit code. A file whose block a person edited
// is left alone; `install --check` keeps reporting it as modified.
func MigrateIfNeeded(dir string) (int, error) {
	st, ok, err := LoadState(dir)
	if err != nil || st.Declined || st.RuleVersion >= Version {
		return 0, err
	}
	seeded := false
	if !ok {
		if !seedFromLegacyPromptStore(dir, &st) {
			return 0, nil
		}
		seeded = true
	}
	migrated := 0
	failed := 0
	dirty := false
	for i := range st.Files {
		f := &st.Files[i]
		// **Labels from the old store were never validated.** Copying one verbatim would send the
		// server a name it refuses (`Claude Code`), losing coverage for that agent silently.
		if normalized := NormalizeAgent(f.Agent); normalized != f.Agent {
			f.Agent = normalized
			dirty = true
		}
		if f.RuleVersion >= Version {
			continue
		}
		// **A deleted file is answered before any guard.** A missing path whose ancestors happen to
		// contain `.git` is still "nothing to migrate", not an unmanaged file: `--check` has to keep
		// saying 「不见了」.
		info, statErr := os.Lstat(f.Path)
		if errors.Is(statErr, fs.ErrNotExist) {
			f.RuleVersion = Version
			dirty = true
			continue
		}
		// **A record this command may never write is settled as unmanaged, not retried forever.**
		// Without this it pins the top-level version (and the reported rule version) at the old
		// value on every future run, and `--check` keeps suggesting an `install --file` that the
		// same guards would refuse.
		if err := refuseSymlink(f.Path); err != nil {
			f.Unmanaged, f.RuleVersion, dirty = true, Version, true
			continue
		}
		// A project-level record is not migrated (PRD 不做 #1): the old `prompt install` could point
		// into a repository, and quietly rewriting it would edit a file that belongs to code.
		if err := refuseProjectLevel(f.Path); err != nil {
			f.Unmanaged, f.RuleVersion, dirty = true, Version, true
			continue
		}
		if statErr == nil && !info.Mode().IsRegular() {
			f.Unmanaged, f.RuleVersion, dirty = true, Version, true
			continue
		}
		content, existed, mode, err := readTarget(f.Path)
		if err != nil {
			failed++
			continue
		}
		// **Rewriting an ignored rule file fixes nothing.** Cursor would still load none of it, and
		// the record would stop being retried while `--check` keeps saying 「读不到」.
		//
		// **Deliberately not settled, either.** Advancing the record would make the migration skip
		// this file forever: once the person adds the frontmatter, `--check` would say 「旧版」 and
		// nothing would replace it. The cost is that `rule_version` in the report stays at the old
		// value until the file is fixed — that is the honest answer for a file nobody reads.
		if mdcNotice(f.Path, content) != "" {
			continue
		}
		if containsBlock(content) {
			if cleaned, removed := stripLegacyOutside(content, Block()); removed > 0 {
				// The current rule is there but old blocks were left beside it; remove only the old.
				// **Same contract as a replacement**: back it up, and only mark it done when the
				// write succeeded — a failed clean must be retried, not recorded as finished.
				backup, err := backupIfNeeded(f.Path, content, existed)
				if err == nil {
					err = writeTarget(f.Path, cleaned, mode)
				}
				if err != nil {
					failed++
					continue
				}
				f.Digest = Digest()
				f.WrittenAt = time.Now().UTC().Format(time.RFC3339)
				if f.Backup == "" {
					f.Backup = backup
				}
				migrated++
			}
			f.RuleVersion = Version
			dirty = true
			continue
		}
		start, end, ok := legacySpanOutside(content, Block())
		if !ok {
			// A person's edit, or a block this build does not know. **Leaving the file alone is
			// right, and leaving its version alone is what lets a later build try again.**
			continue
		}
		// **An edited rule beside an exact old copy is a conflict, not a migration.** Replacing the
		// old copy there would leave the person's text and ours side by side, and recording the new
		// version would stop `--check` from calling the file modified.
		if headingOutsideLegacy(content) {
			continue
		}
		merged := splice(content, start, end, adaptBlock(Block(), content[start:end]))
		// **A second old copy is removed too.** Migrating means "this file ends up with one rule";
		// leaving another old block below the new one would make the next check call it modified.
		merged, _ = stripLegacyOutside(merged, Block())
		backup, err := backupIfNeeded(f.Path, content, existed)
		if err != nil {
			failed++
			continue
		}
		if err := writeTarget(f.Path, merged, mode); err != nil {
			failed++
			continue
		}
		f.RuleVersion = Version
		f.Digest = Digest()
		f.WrittenAt = time.Now().UTC().Format(time.RFC3339)
		if f.Backup == "" {
			f.Backup = backup
		}
		// **The migrated record has to reach disk.** Without this, a sibling record that cannot be
		// migrated keeps the top-level version back, the save is skipped, and every later command
		// rewrites the same file and leaves another backup beside it.
		dirty = true
		migrated++
	}
	before := st.RuleVersion
	syncRuleVersion(&st)
	if dirty || seeded || st.RuleVersion != before {
		if err := SaveState(dir, st); err != nil {
			return migrated, err
		}
	}
	if failed > 0 {
		// **Silence would look like success.** The command still succeeds (ADR 0242), but the person
		// gets one line saying which part of the upgrade did not happen.
		return migrated, fmt.Errorf("%d 个记录文件没有迁移成功（读取、备份或写入失败），下一次会写文件的命令会再试。", failed)
	}
	return migrated, nil
}

// syncRuleVersion moves the state's top-level version only when every record carries the current
// rule. A partial migration must stay visible: `reportRule` reports this number, and a failed file
// has to be retried by the next build instead of being marked as migrated by an unrelated install.
func syncRuleVersion(st *State) {
	for _, f := range st.Files {
		if f.RuleVersion < Version {
			return
		}
	}
	st.RuleVersion = Version
}

// seedFromLegacyPromptStore imports the retired `prompt install` records as unversioned files.
func seedFromLegacyPromptStore(dir string, st *State) bool {
	store, has, err := prompt.LoadStore(dir)
	if err != nil || !has || len(store.Files) == 0 {
		return false
	}
	for _, rec := range store.Files {
		st.Files = append(st.Files, File{Path: rec.File, Agent: NormalizeAgent(rec.Agent)})
	}
	return true
}

// readTarget returns the file's bytes, whether it existed, and the mode to write back.
func readTarget(path string) ([]byte, bool, os.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0o644, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("%s 不是普通文件，不写。", path)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, err
	}
	return blob, true, info.Mode().Perm(), nil
}

// merge appends the block, swaps the exact legacy block for it, or refuses a hand-edited one.
func merge(content, block []byte) ([]byte, string) {
	legacyStart, legacyEnd, hasLegacy := legacySpanOutside(content, block)
	if containsBlock(content) {
		// **A current rule plus a leftover old block is two rule sets.** That is what a person gets
		// from the documented fallback (`prompt print` → paste into the old file → register), and the
		// old block must go; the current one stays exactly where it is.
		if hasLegacy {
			cleaned, _ := stripLegacyOutside(content, block)
			return cleaned, "cleaned"
		}
		return content, "unchanged"
	}
	// **A rule that was edited is not a rule to replace beside.** Writing the current rule next to
	// the person's copy would leave two rule sets, and their words are not ours to overwrite. The
	// test is "a heading that is not an exact old block": every rule text we ever wrote carries the
	// heading, so the bare presence of it means nothing.
	if headingOutsideLegacy(content) {
		return nil, "conflict"
	}
	if hasLegacy {
		// **Every old copy goes, not just the one being replaced.** Replacing the first and leaving a
		// second beside it is the two-rule-sets state again, now with the new one in the middle.
		replaced := splice(content, legacyStart, legacyEnd, adaptBlock(block, content[legacyStart:legacyEnd]))
		replaced, _ = stripLegacyOutside(replaced, block)
		return replaced, "replaced"
	}
	if len(content) == 0 {
		return append([]byte(nil), block...), "created"
	}
	// **The separator follows the file's own ending.** A hardcoded `\n` gave a CRLF file a blank
	// line and mixed newline conventions on its last line.
	sep := lineEnding(content)
	block = adaptBlock(block, content)
	blankLine := append(append([]byte(nil), sep...), sep...)
	if bytes.HasSuffix(content, blankLine) {
		return append(append([]byte(nil), content...), block...), "appended"
	}
	if bytes.HasSuffix(content, sep) {
		return append(append(append([]byte(nil), content...), sep...), block...), "appended"
	}
	return append(append(append([]byte(nil), content...), blankLine...), block...), "appended"
}

// mdcNotice warns when a Cursor rule file is written without frontmatter.
//
// **Cursor silently ignores a `.mdc` that does not open with `---`.** The install would report
// success, `--check` would say 「已安装」, and the agent would still see no rule — the worst shape of
// failure. The CLI cannot make the frontmatter up (it does not know which activation mode the
// person wants), so it says what to do instead.
func mdcNotice(path string, content []byte) string {
	if !strings.HasSuffix(strings.ToLower(path), ".mdc") || hasFrontmatter(content) {
		return ""
	}
	return "这是 Cursor 的 .mdc 规则文件，却没有 frontmatter（文件开头要有 ---，并写明 alwaysApply: true），Cursor 会忽略它。先运行 skmr prompt print 取守则正文，由 Agent 补好 frontmatter 再写入，然后用 skmr install --file <同一路径> --yes 登记。"
}

// hasFrontmatter reports whether the text opens with a `---` block that is closed later on.
func hasFrontmatter(blob []byte) bool {
	switch {
	case bytes.HasPrefix(blob, []byte("---\n")):
		blob = blob[len("---\n"):]
	case bytes.HasPrefix(blob, []byte("---\r\n")):
		blob = blob[len("---\r\n"):]
	default:
		return false
	}
	return bytes.Contains(blob, []byte("\n---"))
}

// splice replaces content[start:end] with repl, without aliasing the original buffer.
func splice(content []byte, start, end int, repl []byte) []byte {
	out := make([]byte, 0, start+len(repl)+len(content)-end)
	out = append(out, content[:start]...)
	out = append(out, repl...)
	return append(out, content[end:]...)
}

// backupIfNeeded copies the pre-write bytes beside the target. A new file has no backup.
func backupIfNeeded(path string, content []byte, existed bool) (string, error) {
	if !existed {
		return "", nil
	}
	backup := path + ".skmr-backup-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(backup, content, 0o600); err != nil {
		return "", fmt.Errorf("写备份 %s：%w", backup, err)
	}
	return backup, nil
}

// writeTarget writes through a temporary sibling and a rename, creating the parent if needed.
//
// **A killed run leaves the old bytes or the new bytes, never half of either.** A direct
// truncate-then-write could destroy the person's file when the disk fills mid-write.
func writeTarget(path string, content []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 %s：%w", filepath.Dir(path), err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".skmr-rule-*")
	if err != nil {
		return fmt.Errorf("写 %s：%w", path, err)
	}
	name := temp.Name()
	// No cleanup on failure: the CLI never deletes files (ADR 0101). A failed write leaves its
	// temporary sibling beside the target, where nothing reads it as an instruction file.
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("写 %s：%w\n（半成品留在 %s，没有删掉——确认后请自行删除。）", path, err, name)
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("写 %s：%w", path, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("写 %s：%w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("写 %s：%w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("保存到 %s：%w\n（写好的内容留在 %s，没有删掉——确认后请自行删除。）", path, err, name)
	}
	return nil
}

// NormalizeAgent turns `--agent` into the one shape the record and the report use.
//
// **The server accepts ASCII slug-like labels, and the docs ask for "the agent's name".** A person
// typing `Claude Code` must not lose coverage to a 400, so the label is folded here: lower case,
// spaces to hyphens, anything else outside the allowed set dropped.
func NormalizeAgent(agent string) string {
	trimmed := strings.TrimSpace(agent)
	if trimmed == "" {
		return "unknown"
	}
	var out strings.Builder
	for _, r := range strings.ToLower(trimmed) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			out.WriteRune(r)
		case r == ' ' || r == '\t':
			out.WriteByte('-')
		}
		if out.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(out.String(), "-.")
	if slug == "" {
		return "unknown"
	}
	return slug
}

func refuseSymlink(path string) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s 是符号链接，不跟着它写。用它指向的路径重新运行。", path)
	}
	return nil
}

// refuseProjectLevel keeps the CLI to user-level instruction files.
//
// **The product promise is "user-level only"** (PRD 不做 #1): a project file belongs to a repository.
// The test walks up from the target's directory and stops at the home directory: most projects live
// *under* home (`~/Developer/app`), so an "under home is fine" shortcut would miss exactly the case
// the promise is about. Home itself is the exception — a dotfiles repository at `~` versions the
// person's own files, and `~/.claude/CLAUDE.md` is still a user-level instruction file.
//
// **`.git` may be a file**, as it is in a linked worktree or a submodule; `IsDir` would miss those.
func refuseProjectLevel(path string) error {
	// **Both spellings of home are the exception.** A home reached through a symlink (macOS
	// `/var` → `/private/var`, a moved home directory) compares equal only after resolving.
	homes := map[string]bool{}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		clean := filepath.Clean(home)
		homes[clean] = true
		homes[resolvePath(clean)] = true
	}
	// **Resolve the directory chain before asking whether it is in a repository.** A symlinked
	// parent (`~/.claude` → a dotfiles checkout, or → a project) would otherwise make the walk look
	// outside the repository whose file the write actually lands in.
	dir := resolvePath(filepath.Clean(filepath.Dir(path)))
	for {
		if homes[dir] {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("%s 在 Git 仓库里，属于项目级文件；本命令只写用户级文件。请换成 Agent 的全局指令文件。", path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// resolvePath resolves symlinks in the longest existing prefix and appends the rest unchanged.
//
// **A path that does not exist yet still has to be judged by where it will land.** Resolving only
// the whole path would leave `~/.claude/CLAUDE.md` (with `~/.claude` missing) as a lexical string,
// and a symlinked home would stop matching the home exception or hide a parent repository.
func resolvePath(path string) string {
	var rest []string
	p := path
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return resolved
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		rest = append(rest, filepath.Base(p))
		p = parent
	}
}

func planFor(path, action string) string {
	switch action {
	case "unchanged":
		return "已是最新：" + path
	case "cleaned":
		return "移除了重复的旧守则：" + path
	case "replaced":
		return "替换旧守则：" + path
	case "created":
		return "新建并写入守则：" + path
	case "conflict":
		return "文件里已有一段被改过的守则：" + path
	default:
		return "追加守则：" + path
	}
}
