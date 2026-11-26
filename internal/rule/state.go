package rule

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is one instruction file this machine has written.
type File struct {
	// Path is the absolute path the person pointed `--file` at.
	Path string `json:"path"`
	// Agent is the label they gave (`--agent`), or "unknown".
	Agent string `json:"agent,omitempty"`
	// RuleVersion is the rule text last written into it.
	RuleVersion int `json:"rule_version"`
	// Digest is the digest of the rule text, read back after writing.
	Digest string `json:"digest"`
	// WrittenAt is when the last write happened, RFC 3339 UTC.
	WrittenAt string `json:"written_at"`
	// Backup is the copy `--undo` restores, when the file existed before the first write.
	Backup string `json:"backup,omitempty"`
	// Created is true only when this CLI created the file. `--undo` may leave such a file empty;
	// a file that already existed is restored from Backup or not touched at all (ADR 0101's
	// "empty file" is for files this command made).
	Created bool `json:"created,omitempty"`
	// Unmanaged marks a record this command will never write: the path is inside a Git work tree,
	// is a symbolic link, or is not a regular file. It is settled at the current rule version so it
	// cannot pin the state forever, and `--check` reports it as out of scope instead of "被改过".
	// `--undo` removes such a record without touching the file.
	Unmanaged bool `json:"unmanaged,omitempty"`
}

// State is `install.json`: what this machine has installed, for detection and migration.
//
// **A separate file from `prompt.json`.** That one is the frozen history of the retired `prompt
// install` commands (ADR 0110); this one is live state and is rewritten whenever the rule changes.
type State struct {
	Schema      int    `json:"schema"`
	RuleVersion int    `json:"rule_version,omitempty"`
	Declined    bool   `json:"declined,omitempty"`
	Files       []File `json:"files"`
}

// StatePath is where the state lives, beside the credential and the pins.
func StatePath(dir string) string { return filepath.Join(dir, "install.json") }

// stateReadPath is a second place the records file may be read from when dir has none.
//
// **Reading and writing are two different questions** — the same split `config.StatePaths` makes
// for the credential. A sandbox makes the ordinary directory read-only, so `config.Dir()` falls back
// to a writable temp directory that holds no records, while the real ones sit next door and are
// perfectly readable. Without this fallback `install --check` inside a sandbox answers "这台电脑还没
// 有安装记录" on a machine that has three (measured with sandbox-exec, 2026-11-26). Writes never
// follow this path.
var stateReadPath string

// SetStateReadPath lets reads fall back to an existing records file elsewhere. Writes stay in dir.
// The command layer sets this once per process; tests that set it must restore it.
func SetStateReadPath(path string) { stateReadPath = path }

// stateSource is the file LoadState reads: the directory's own when it exists, else the fallback.
func stateSource(dir string) string {
	own := StatePath(dir)
	if stateReadPath == "" {
		return own
	}
	if _, err := os.Stat(own); err == nil {
		return own
	}
	return stateReadPath
}

// LoadState reads the state. A missing file is "nothing installed", not an error.
func LoadState(dir string) (State, bool, error) {
	source := stateSource(dir)
	blob, err := os.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		return State{Schema: 1}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("读取 %s：%w", source, err)
	}
	var st State
	if err := json.Unmarshal(blob, &st); err != nil {
		// **A mangled state file is an error, not "nothing installed".** Reading it as empty would
		// drop every record, report the machine as uncovered, and let the next write replace what a
		// person installed. Saying so lets them move the file aside.
		return State{}, false, fmt.Errorf("读取 %s：文件内容不是有效的 install.json（%v）；请检查或把它移走。", source, err)
	}
	if st.Schema == 0 {
		st.Schema = 1
	}
	return st, true, nil
}

// SaveState writes the state, creating the directory when it is missing.
//
// **Temporary file then rename.** Two agents running `skmr` at once (parallel tool calls) can both
// save; a direct truncate-and-write would let one of them read half a file and conclude nothing was
// ever installed. The rename makes a reader see either the old state or the new one.
func SaveState(dir string, st State) error {
	st.Schema = 1
	blob, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".install-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	// **Nothing is deleted on failure** (ADR 0101): a half-written temporary file stays beside the
	// state, where nothing reads it, rather than the CLI removing a file it made.
	if _, err := temp.Write(append(blob, '\n')); err != nil {
		temp.Close()
		return err
	}
	// **Land the bytes before the rename.** Without the fsync a power cut can leave the renamed file
	// empty, which reads as "nothing installed" on the next run.
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
	return os.Rename(name, StatePath(dir))
}

// Put replaces the record for f.Path, or appends it.
func (s *State) Put(f File) {
	for i := range s.Files {
		if s.Files[i].Path == f.Path {
			s.Files[i] = f
			return
		}
	}
	s.Files = append(s.Files, f)
}

// Remove drops the record for path.
func (s *State) Remove(path string) {
	out := s.Files[:0]
	for _, f := range s.Files {
		if f.Path != path {
			out = append(out, f)
		}
	}
	s.Files = out
}

// Find returns the record for path.
func (s State) Find(path string) (File, bool) {
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return File{}, false
}
