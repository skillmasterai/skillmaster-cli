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
)

// ExpandPath turns a typed path into an absolute one.
//
// **Only `~` is expanded, and only at the front.** The value is what the person typed; a shell that
// already expanded it hands over the absolute form, and anything else is a path, not a template.
func ExpandPath(path string) (string, error) {
	// **Backslash after `~` on Windows only.** A path typed there, or by an agent quoting one, is
	// `~\.codex\AGENTS.md`; `filepath.Abs` would treat the `~` as a directory name.
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

// FileHasRule reports whether path carries the current rule the editor will actually read.
//
// **It follows a symlink, as v94's default-path answer did.** A dotfiles layout
// (`~/.claude/CLAUDE.md` -> `~/dotfiles/CLAUDE.md`) is read by the editor exactly like a regular
// file; the coverage answer is about what the editor sees, not about where the bytes live.
//
// **A Cursor rule file the editor ignores is not coverage.** `.mdc` without frontmatter is loaded by
// nothing, so the block being there would report a rule the agent never sees.
func FileHasRule(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	blob, err := os.ReadFile(path)
	if err != nil || !containsBlock(blob) {
		return false
	}
	return mdcNotice(path, blob) == ""
}

// PathIsManageable reports whether path is a file the CLI may read as an instruction file: a
// missing path counts, a regular file counts, and anything else — a symlink, a directory, a socket —
// does not.
func PathIsManageable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return true
	}
	return info.Mode().IsRegular()
}

// CheckPath answers one file's state for `skmr self rule check`.
//
// The states are the ones `install --check` reported before 1.10.1, because agents and docs already use
// their words: present / legacy / ignored / absent / modified / unmanaged.
func CheckPath(path string) (string, string) {
	abs, err := ExpandPath(path)
	if err != nil {
		return "absent", err.Error()
	}
	if !PathIsManageable(abs) {
		return "unmanaged", "路径是符号链接或不是普通文件"
	}
	// **A file inside a Git repository is project-level, and this command does not judge it**
	// (ADR 0249 §后果; the words `self rule check --help` promises). The home directory itself is
	// excluded: a dotfiles checkout makes `~` a repository, and `~/.claude/CLAUDE.md` there is still
	// the person's user-level file.
	if projectLevel(abs) {
		return "unmanaged", "在 Git 仓库里：项目级文件"
	}
	blob, err := os.ReadFile(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "absent", "文件不存在"
	case err != nil:
		return "absent", err.Error()
	case mdcNotice(abs, blob) != "":
		// **The editor reads none of it, whatever the bytes say.** Checking for the rule first
		// would answer 「被改过」 about a file Cursor ignores entirely, and send the agent to rewrite
		// the text instead of adding frontmatter.
		return "ignored", mdcNotice(abs, blob)
	case containsBlock(blob):
		if _, _, leftover := legacySpanOutside(blob, Block()); leftover {
			return "present", "旁边还有一段旧守则"
		}
		return "present", ""
	case headingOutsideLegacy(blob):
		return "modified", "守则被改过，旁边可能还有一段旧守则"
	case legacyBlockIndex(blob) != nil:
		return "legacy", "旧版守则"
	default:
		// **A file that never carried the rule is not "edited".** The person may have a notes file
		// where the agent is about to write; telling them 「守则被改过或删掉了」 invents a history.
		return "absent", "文件里没有 SkillMaster 守则"
	}
}

// projectLevel reports whether abs sits inside a Git repository below the home directory.
//
// **Below home, not at home.** A dotfiles checkout makes `$HOME` itself a repository; the files this
// command is asked about (`~/.claude/CLAUDE.md`) are user-level even then, so the walk stops at home
// before looking for `.git`.
//
// **Resolve what exists before walking.** `~/.claude` is often a symlink into a dotfiles checkout
// (GNU stow); the file lives in a repository even though the typed path does not show one. The
// longest existing prefix is resolved, so that layout is judged as what it is.
func projectLevel(abs string) bool {
	home := ""
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		home = filepath.Clean(dir)
	}
	for dir := existingDir(filepath.Dir(abs)); ; dir = filepath.Dir(dir) {
		if home != "" && samePath(dir, home) {
			return false
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
	}
}

// existingDir is the resolved form of dir, or of its nearest ancestor that exists.
func existingDir(dir string) string {
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return resolved
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// samePath compares two directories after resolving links, so a symlinked or trailing-separator
// `$HOME` still equals the home the walk reaches.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" && strings.EqualFold(a, b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
}

// mdcNotice names the frontmatter problem when a Cursor rule file would be ignored.
func mdcNotice(path string, content []byte) string {
	if !strings.HasSuffix(strings.ToLower(path), ".mdc") || hasFrontmatter(content) {
		return ""
	}
	return "这是 Cursor 的 .mdc 规则文件，却没有 frontmatter（文件开头要有 ---，并写明 alwaysApply: true），Cursor 会忽略它。"
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

// NormalizeAgent turns an agent label into the one shape the report uses.
//
// **The server accepts ASCII slug-like labels, and the docs ask for "the agent's name".** A person
// typing `Claude Code` must not lose coverage to a 400, so the label is folded here.
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
