package rule

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// HintStatePath is the small file that remembers which rule version was last hinted about.
//
// **Separate from `install.json` on purpose** (ADR 0246): the ledger answers "which files did this
// command write"; the hint throttle answers "has this machine already been told". Putting the
// second into the first would make `install --check`'s 「还没有安装记录」 depend on a notice nobody
// asked for.
//
// **It lives where commands write, not where the ledger is read from.** Inside a sandbox the two
// can differ, and a sandbox run then repeats the notice its host already printed — once, per rule
// version, which is the same cost as a lost write. Following `stateReadPath` instead would let a
// read-only sandbox spend the host's notice without the host seeing it.
func HintStatePath(dir string) string {
	return filepath.Join(dir, "rule-hint.json")
}

type hintState struct {
	Schema        int `json:"schema"`
	HintedVersion int `json:"hinted_version"`
}

// HintedVersion is the last rule version this machine was hinted about; 0 when never, or unreadable
// (a notice is cheap to repeat once, and a broken file must not silence the next one).
func HintedVersion(dir string) int {
	blob, err := os.ReadFile(HintStatePath(dir))
	if err != nil {
		return 0
	}
	var st hintState
	if err := json.Unmarshal(blob, &st); err != nil {
		return 0
	}
	return st.HintedVersion
}

// MarkHinted records the version; failure is silent because the caller's command already succeeded
// and the next command simply tries again.
//
// **The file is synced before the rename**, as `SaveState` is: a crash between write and rename
// would otherwise leave an empty `rule-hint.json`, which reads as "not hinted" and repeats the
// notice once. Two commands that start at the same moment can both pass the read and both print —
// accepted in the test plan's known issues; a lock would cost every command a file syscall.
func MarkHinted(dir string, version int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	blob, err := json.Marshal(hintState{Schema: 1, HintedVersion: version})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".rule-hint-*.json")
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
	if err := os.Rename(name, HintStatePath(dir)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}
