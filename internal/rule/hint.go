package rule

import (
	"os"
	"path/filepath"
	"strings"
)

// HintKind is what, if anything, the machine's rule needs from the agent.
type HintKind int

const (
	// HintNone means the rule is current where it matters (or the machine declined).
	HintNone HintKind = iota
	// HintRecordedMissing: a file this command wrote has nothing usable — emptied, edited by hand,
	// or written without the frontmatter an editor needs.
	HintRecordedMissing
	// HintUnreadableDefault: a known default instruction file carries the rule, but the editor
	// loads none of it (a Cursor `.mdc` without frontmatter).
	HintUnreadableDefault
	// HintOutdatedDefault: a known default instruction file holds an exact old rule.
	HintOutdatedDefault
	// HintNothingInstalled: no recorded file and no default file has the rule.
	HintNothingInstalled
	// HintUnreadable: `install.json` could not be read, so nothing can be said about this machine.
	// Nothing is printed and the version is not marked — a broken ledger must not spend the notice.
	HintUnreadable
)

// Hint is one line's worth of "what to do about the rule".
type Hint struct {
	Kind  HintKind
	Path  string
	Agent string
}

// NeedHint answers from what is on disk right now, not from the ledger alone (ADR 0246).
//
// **The ledger says who wrote what; the files say whether the rule is there.** It uses the same
// answers the rest of the package gives — `FileHasRule` for recorded files, `mdcNotice` for Cursor
// rule files — so the notice cannot call 「已装」 what `install --check` calls 「读不到」. Nothing
// is written here.
func NeedHint(dir string) Hint {
	st, _, err := LoadState(dir)
	if err != nil {
		// **No answer is not "nothing installed".** Reporting a machine with no rule because its
		// ledger would not parse is a false fact, and the notice would then mark the version spent.
		return Hint{Kind: HintUnreadable}
	}
	installed := false
	var missing, unreadable, outdated *Hint
	for _, f := range st.Files {
		// A record with no path cannot be checked; it would otherwise produce `--file  --agent` .
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		// Out-of-scope records (repository files, links, directories) are not the person's rule.
		if f.Unmanaged || !PathIsManageable(f.Path) {
			continue
		}
		if FileHasRule(f.Path) {
			installed = true
			continue
		}
		// **Only the first missing one is named.** One line, one command; the rest are named by the
		// next command's check or by `install --check`, which lists every record.
		if missing == nil {
			missing = &Hint{Kind: HintRecordedMissing, Path: f.Path, Agent: NormalizeAgent(f.Agent)}
		}
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
		for _, entry := range defaultInstructionFiles {
			candidates, override := entry.Paths(home)
			var paths []string
			for _, candidate := range candidates {
				matched, _ := filepath.Glob(candidate)
				paths = append(paths, matched...)
			}
			// Same expansion as `DefaultPresent`: the file the client reads preferentially is the
			// only one that counts when it exists.
			if override != "" {
				if _, err := os.Stat(override); err == nil {
					paths = []string{override}
				}
			}
			for _, path := range paths {
				blob, readErr := os.ReadFile(path)
				if readErr != nil {
					continue
				}
				// **The editor's answer is what counts** (v94's `FileHasRule`): bytes an editor
				// ignores are not a rule. A `.mdc` without frontmatter is its own case because the
				// bytes are there and the fix is to add the frontmatter, not to install again.
				ignored := mdcNotice(path, blob) != ""
				if containsBlock(blob) {
					if ignored {
						if unreadable == nil {
							unreadable = &Hint{Kind: HintUnreadableDefault, Path: path, Agent: entry.Agent}
						}
						continue
					}
					installed = true
					continue
				}
				// **An edit outranks an old copy beside it**, the order `Check` and `merge` use: a
				// file whose heading was rewritten is refused by `install`, so calling it 「旧版」
				// would point at a command whose answer is no.
				if headingOutsideLegacy(blob) {
					installed = true
					continue
				}
				if legacyBlockIndex(blob) != nil {
					// **The frontmatter is missing in either version.** An ignored `.mdc` whose old
					// rule cannot be read is the unreadable case, not 「登记并更新」.
					if ignored {
						if unreadable == nil {
							unreadable = &Hint{Kind: HintUnreadableDefault, Path: path, Agent: entry.Agent}
						}
						continue
					}
					if outdated == nil {
						outdated = &Hint{Kind: HintOutdatedDefault, Path: path, Agent: entry.Agent}
					}
				}
			}
		}
	}
	// **A gap outranks a healthy file.** Two records can disagree — Claude has the current rule and
	// the file Codex was told about was emptied — and that is exactly the machine this notice is
	// for; "something on this machine is current" must not swallow it. The healthy files only
	// decide the last two answers.
	switch {
	case missing != nil:
		return *missing
	case unreadable != nil:
		return *unreadable
	case outdated != nil:
		return *outdated
	case installed:
		return Hint{Kind: HintNone}
	default:
		return Hint{Kind: HintNothingInstalled}
	}
}
