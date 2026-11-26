package rule

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// StateFile is the one local file this package keeps: three scalars, no paths, no text (ADR 0249).
//
// **It is not a ledger.** v94's `install.json` answered "which files did this CLI write"; 1.10.1 has
// no such writer, so what is left is the throttles: when the last report went out, which rule
// version was last announced, and whether the person said "stop asking".
const StateFile = "rule-state.json"

// ReportInterval is how often a machine reports coverage.
const ReportInterval = 24 * time.Hour

// LocalState is `rule-state.json`.
type LocalState struct {
	Schema        int    `json:"schema"`
	ReportedAt    string `json:"reported_at"`
	HintedVersion int    `json:"hinted_version"`
	Declined      bool   `json:"declined"`
}

// LocalStatePath is where the state file lives under this run's config directory.
func LocalStatePath(dir string) string { return filepath.Join(dir, StateFile) }

// LoadLocalState reads the file, seeding it from the two files v96 left behind when it is absent.
//
// **A missing file is a fresh machine, not an error.** The seed exists so an upgrade does not repeat
// a report the 1.x binary already sent, or re-announce a rule version it already announced; the old
// files are left where they are (nothing reads them again).
func LoadLocalState(dir string) (LocalState, bool, error) {
	blob, err := os.ReadFile(LocalStatePath(dir))
	if err == nil {
		var st LocalState
		if err := json.Unmarshal(blob, &st); err != nil {
			// **A torn write is no state, not a dead end.** Saving in place can leave a partial
			// file; treating it as fresh lets the next save repair it. The cost is one extra
			// notice or report, never permanent silence.
			return LocalState{Schema: 2}, false, nil
		}
		return st, true, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return LocalState{}, false, err
	}
	st := LocalState{Schema: 2}
	if blob, err := os.ReadFile(filepath.Join(dir, "rule-report.json")); err == nil {
		var old struct {
			SentAt string `json:"sent_at"`
		}
		if json.Unmarshal(blob, &old) == nil {
			st.ReportedAt = old.SentAt
		}
	}
	if blob, err := os.ReadFile(filepath.Join(dir, "rule-hint.json")); err == nil {
		var old struct {
			HintedVersion int `json:"hinted_version"`
		}
		if json.Unmarshal(blob, &old) == nil {
			st.HintedVersion = old.HintedVersion
		}
	}
	return st, false, nil
}

// SaveLocalState writes the file in place; failure is the caller's to ignore, because a lost
// throttle only means the next command tries again.
//
// **In place, not temp-and-rename.** Replacing an existing name asks the filesystem to delete it,
// and a sandboxed agent (WorkBuddy 5.8.6, measured 2026-11-11 for the binary) allows writes to the
// state directory but not that delete. The save would fail silently and every command would repeat
// the notice or the report. A torn write is a corrupt file, which LoadLocalState treats as "no
// state", so the next save repairs it.
func SaveLocalState(dir string, st LocalState) error {
	st.Schema = 2
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	blob, err := json.Marshal(st)
	if err != nil {
		return err
	}
	path := LocalStatePath(dir)
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// ReportDue says whether the throttle has elapsed. An unreadable or unparseable timestamp counts as
// due: a broken state file must not silence reporting for ever.
func ReportDue(st LocalState, now time.Time) bool {
	if st.ReportedAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, st.ReportedAt)
	if err != nil {
		return true
	}
	return now.Sub(last) >= ReportInterval
}

// MarkReported records the attempt. **Before the request, not after** (v94): a dead endpoint must
// not turn "once a day" into "once per command".
func MarkReported(dir string, now time.Time) {
	st, _, err := LoadLocalState(dir)
	if err != nil {
		st = LocalState{Schema: 2}
	}
	st.ReportedAt = now.UTC().Format(time.RFC3339)
	_ = SaveLocalState(dir, st)
}

// SetDeclined records the person's answer to "stop asking" and nothing else.
func SetDeclined(dir string, declined bool) error {
	st, _, err := LoadLocalState(dir)
	if err != nil {
		return err
	}
	st.Declined = declined
	return SaveLocalState(dir, st)
}
