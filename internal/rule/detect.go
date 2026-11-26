package rule

import (
	"os"
	"path/filepath"
)

// Detection is one client's answer: does the file it reads carry the current rule?
type Detection struct {
	Agent string
	// Path is the file the answer came from; empty when the client has no file on this machine.
	Path string
	// Present is true only when the editor reads the current rule (v94's `FileHasRule`: a `.mdc`
	// without frontmatter is loaded by nothing).
	Present bool
}

// Detect scans the known default locations and answers per client.
//
// **Read-only and stateless** (ADR 0249): no ledger, no writes. A client counts as detected when it
// has a file at a known location — an empty `MEMORY.md` is still "this machine has WorkBuddy, and
// its rule is not there", which is the fact coverage is about. Clients with no file are left out;
// the caller decides what an empty answer means.
func Detect(home string) []Detection {
	if home == "" {
		return nil
	}
	var out []Detection
	for _, entry := range defaultInstructionFiles {
		candidates, override := entry.Paths(home)
		var paths []string
		for _, candidate := range candidates {
			matched, _ := filepath.Glob(candidate)
			paths = append(paths, matched...)
		}
		// Same expansion as v94's `DefaultPresent`: the file the client reads preferentially is the
		// only one that counts when it exists.
		if override != "" {
			if _, err := os.Stat(override); err == nil {
				paths = []string{override}
			}
		}
		detection := Detection{Agent: NormalizeAgent(entry.Agent)}
		for _, path := range paths {
			// **Stat, not Lstat**: a dotfiles symlink is what the editor reads, so it is coverage.
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if detection.Path == "" {
				detection.Path = path
			}
			if FileHasRule(path) {
				detection.Path = path
				detection.Present = true
				break
			}
		}
		if detection.Path != "" {
			out = append(out, detection)
		}
	}
	return out
}
