package rule

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultInstructionFiles are the global instruction files of agents this build knows by heart.
//
// **Reporting only** (ADR 0242): the account-level report reads these paths to answer
// `default_rule_present`; `doctor` does not print them, and no command writes one: the agent does,
// after the person agrees. Paths that have not been tested on a real installation do not belong
// here.
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
		//
		// **Bound is "a user directory exists", not "a MEMORY.md exists in it".** A login that has
		// not written its memory file yet still reads only the user file; falling back to the root
		// then is exactly the false coverage ADR 0249 removes.
		if userDirs, _ := filepath.Glob(filepath.Join(root, "user-*")); anyDirectory(userDirs) {
			return []string{filepath.Join(root, "user-*", "MEMORY.md")}, ""
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

// anyDirectory reports whether at least one match is a directory. A stray `user-notes` file beside
// the config directory must not mean "a user is bound".
func anyDirectory(paths []string) bool {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
