package rule

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultInstructionFiles are the global instruction files of agents this build knows by heart.
//
// **Reporting only** (ADR 0242): the account-level report reads these paths to answer
// `default_rule_present`; `doctor` does not print them, and `install` never writes one unless a
// person passed it as `--file`. Paths that have not been tested on a real installation do not
// belong here.
//
// A client with a config-directory environment variable resolves it here, because the documented
// install path does too — detecting only the home default would report those machines as absent.
//
// WorkBuddy keeps one memory directory per signed-in user: `readLocalMemoryFiles` resolves its
// config dir through `WorkbuddyPathService.userDir`, which is `<root>/user-<uid>-personal` once a
// user is bound and `<root>` itself only before login (app.asar, read 2026-11-26). So the default
// is a glob across user directories, with the unbound root file kept as a fallback.
var defaultInstructionFiles = []struct {
	Agent string
	// Paths returns the candidate files under a home directory (globs allowed), followed by the
	// override file when the client reads one preferentially.
	Paths func(home string) (paths []string, override string)
}{
	{Agent: "claude-code", Paths: func(home string) ([]string, string) {
		return []string{filepath.Join(home, ".claude", "CLAUDE.md")}, ""
	}},
	{Agent: "codex", Paths: func(home string) ([]string, string) {
		root := codexHome(home)
		return []string{filepath.Join(root, "AGENTS.md")}, filepath.Join(root, "AGENTS.override.md")
	}},
	{Agent: "workbuddy", Paths: func(home string) ([]string, string) {
		root := workbuddyDir(home)
		// **A fallback, not an "or".** Once a user is bound the app reads only
		// `user-<id>-personal/MEMORY.md`; a rule left in the root file from before login is one the
		// agent no longer sees, and counting it would report coverage that is not there.
		perUser := filepath.Join(root, "user-*", "MEMORY.md")
		if matched, _ := filepath.Glob(perUser); len(matched) > 0 {
			return []string{perUser}, ""
		}
		return []string{filepath.Join(root, "MEMORY.md")}, ""
	}},
	// **Cursor reads machine-local rule files, and only sometimes.** `~/.cursor/rules/*.mdc` is
	// found by walking up from the open project, so it applies only when that project is under the
	// home directory; the UI never lists these files. Cursor's account-synced "User Rules" are the
	// other half and are not a file at all. Measured on Cursor 3.32.4, 2026-11-26: with a project
	// under $HOME the rule is read; with an empty window it is not.
	{Agent: "cursor", Paths: func(home string) ([]string, string) {
		return []string{filepath.Join(home, ".cursor", "rules", "*.mdc")}, ""
	}},
}

// workbuddyDir is where WorkBuddy keeps its local memory files.
func workbuddyDir(home string) string {
	if dir := strings.TrimSpace(os.Getenv("WORKBUDDY_CONFIG_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(home, ".workbuddy")
}

// codexHome is where Codex keeps its global files: `CODEX_HOME` when set, `~/.codex` otherwise.
func codexHome(home string) string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// DefaultPresent reports whether any known default instruction file carries the current rule.
//
// known is false when the machine has none of the files: "no file to look at" is not the same fact
// as "the rule is absent". The report's `default_rule_present` is a required boolean on the wire,
// so it carries false either way; what keeps that from reading as "checked and absent" is the row
// around it (see `reportRule`, which pairs it with a null `agent_rule_present` on a machine with no
// record). `known` stays in the signature because that distinction is real here even when the wire
// cannot take it.
func DefaultPresent(home string) (present, known bool) {
	for _, entry := range defaultInstructionFiles {
		candidates, override := entry.Paths(home)
		var paths []string
		for _, candidate := range candidates {
			matched, _ := filepath.Glob(candidate)
			paths = append(paths, matched...)
		}
		if override != "" {
			if _, err := os.Stat(override); err == nil {
				paths = []string{override}
			}
		}
		for _, path := range paths {
			known = true
			blob, err := os.ReadFile(path)
			// **A Cursor rule file the editor ignores is not coverage here either.** `FileHasRule`
			// and `Check` already refuse to count it; the default-path answer must agree, or one
			// report row says "present at the default path" and "not installed" about one file.
			if err == nil && containsBlock(blob) && mdcNotice(path, blob) == "" {
				return true, true
			}
		}
	}
	return false, known
}
