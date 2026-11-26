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
			return LocalState{}, false, err
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

// SaveLocalState writes the file atomically; failure is the caller's to ignore, because a lost
// throttle only means the next command tries again.
func SaveLocalState(dir string, st LocalState) error {
	st.Schema = 2
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	blob, err := json.Marshal(st)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".rule-state-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	// A half-written temporary file stays where it is rather than being deleted (ADR 0101).
	if _, err := temp.Write(append(blob, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, LocalStatePath(dir)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
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
