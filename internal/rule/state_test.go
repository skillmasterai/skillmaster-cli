package rule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := LocalState{Schema: 2, ReportedAt: "2026-11-26T12:00:00Z", HintedVersion: Version, Declined: true}
	if err := SaveLocalState(dir, want); err != nil {
		t.Fatal(err)
	}
	got, known, err := LoadLocalState(dir)
	if err != nil || !known {
		t.Fatalf("LoadLocalState: %+v, known = %v, err = %v", got, known, err)
	}
	if got != want {
		t.Errorf("state = %+v, want %+v", got, want)
	}
	info, err := os.Stat(LocalStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// **The save must not replace the file.** A sandbox that allows writes but not deletes refuses a
// rename over an existing name; the state would never persist and the notice or report would
// repeat on every command.
func TestSaveLocalStateOverwritesInPlace(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLocalState(dir, LocalState{Schema: 2, HintedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(LocalStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveLocalState(dir, LocalState{Schema: 2, HintedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(LocalStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the save replaced the file; a sandbox that allows writes but not deletes would refuse")
	}
	st, _, err := LoadLocalState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.HintedVersion != 2 {
		t.Errorf("hinted_version = %d, want 2", st.HintedVersion)
	}
}

// **An older file that was writable by others is narrowed, not left wide.** Writing in place keeps
// the existing mode, so the save has to set 0600 itself; a sandbox that refuses chmod must not turn
// a successful save into an error, which is why the failure is ignored.
func TestSaveLocalStateNarrowsAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := LocalStatePath(dir)
	if err := os.WriteFile(path, []byte(`{"schema":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveLocalState(dir, LocalState{Schema: 2, HintedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 after the save", info.Mode().Perm())
	}
}

// **A torn in-place write is recovered, not fatal.** The next save repairs it; the cost is one
// extra notice or report, never permanent silence.
func TestLoadLocalStateTreatsACorruptFileAsFresh(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(LocalStatePath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, known, err := LoadLocalState(dir)
	if err != nil || known || st.Schema != 2 {
		t.Fatalf("state = %+v, known = %v, err = %v; want a fresh state", st, known, err)
	}
	if err := SaveLocalState(dir, LocalState{Schema: 2, HintedVersion: Version}); err != nil {
		t.Fatal(err)
	}
	st, known, err = LoadLocalState(dir)
	if err != nil || !known || st.HintedVersion != Version {
		t.Errorf("state = %+v, known = %v, err = %v; want the repaired state", st, known, err)
	}
}

// **The file is four scalars, all of them present.** A path or a file name appearing here would be
// the ledger again, which is the thing 1.10.1 removed (ADR 0249 §决定 3). An empty `reported_at`
// is still a key: "never reported" and "field absent" must not become two spellings of the file.
func TestLocalStateHoldsOnlyTheThrottles(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLocalState(dir, LocalState{Schema: 2}); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(LocalStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"schema": true, "reported_at": true, "hinted_version": true, "declined": true}
	for key := range fields {
		if !want[key] {
			t.Errorf("the state file carries %q", key)
		}
	}
	for key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("the state file is missing %q", key)
		}
	}
}

// **An upgrade does not repeat what 1.x already did.** The first 1.10.1 run reads the two old files
// once, and leaves them where they are.
func TestLoadLocalStateSeedsFromTheLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	report := []byte(`{"sent_at":"2026-11-20T08:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(dir, "rule-report.json"), report, 0o644); err != nil {
		t.Fatal(err)
	}
	hint := []byte(`{"hinted_version":2}`)
	if err := os.WriteFile(filepath.Join(dir, "rule-hint.json"), hint, 0o644); err != nil {
		t.Fatal(err)
	}
	st, known, err := LoadLocalState(dir)
	if err != nil || known {
		t.Fatalf("known = %v, err = %v; want a fresh state", known, err)
	}
	if st.ReportedAt != "2026-11-20T08:00:00Z" || st.HintedVersion != 2 {
		t.Errorf("state = %+v, want the old values seeded", st)
	}
	for _, name := range []string{"rule-report.json", "rule-hint.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

// A corrupt old file seeds nothing; it must not stop the new state from working either.
func TestLoadLocalStateIgnoresCorruptLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rule-report.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rule-hint.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, known, err := LoadLocalState(dir)
	if err != nil || known {
		t.Fatalf("known = %v, err = %v", known, err)
	}
	if st.ReportedAt != "" || st.HintedVersion != 0 {
		t.Errorf("state = %+v, want an empty seed", st)
	}
}

// **An unparseable timestamp is due.** A broken state file must not silence reporting for ever.
func TestReportDue(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		st   LocalState
		want bool
	}{
		{"never reported", LocalState{}, true},
		{"an hour ago", LocalState{ReportedAt: now.Add(-time.Hour).Format(time.RFC3339)}, false},
		{"23 hours ago", LocalState{ReportedAt: now.Add(-23 * time.Hour).Format(time.RFC3339)}, false},
		{"exactly a day ago", LocalState{ReportedAt: now.Add(-ReportInterval).Format(time.RFC3339)}, true},
		{"unparseable", LocalState{ReportedAt: "yesterday"}, true},
	}
	for _, tc := range cases {
		if got := ReportDue(tc.st, now); got != tc.want {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// **MarkReported keeps the other two fields.** A report must not clear a refusal the person stated.
func TestMarkReportedKeepsTheOtherFields(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLocalState(dir, LocalState{Schema: 2, HintedVersion: 2, Declined: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	MarkReported(dir, now)
	st, _, err := LoadLocalState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.ReportedAt != now.Format(time.RFC3339) || st.HintedVersion != 2 || !st.Declined {
		t.Errorf("state = %+v, want only reported_at changed", st)
	}
}

func TestSetDeclinedWritesOnlyThatField(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLocalState(dir, LocalState{Schema: 2, HintedVersion: Version}); err != nil {
		t.Fatal(err)
	}
	if err := SetDeclined(dir, true); err != nil {
		t.Fatal(err)
	}
	st, _, err := LoadLocalState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Declined || st.HintedVersion != Version {
		t.Errorf("state = %+v, want declined set and the rest kept", st)
	}
}

// No state file at all is not an error: it is the first run on a machine.
func TestLoadLocalStateOnAFreshMachine(t *testing.T) {
	st, known, err := LoadLocalState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if known || st.Schema != 2 || st.Declined || st.HintedVersion != 0 {
		t.Errorf("state = %+v, known = %v, want a fresh schema-2 state", st, known)
	}
}

// **Every version this binary carries has a note.** The update notice quotes it; a version with no
// note would fall back to "update the rule" with no diff, the shape of an injection.
func TestTheCurrentRuleVersionHasANote(t *testing.T) {
	if VersionNote(Version) == "" {
		t.Errorf("rule.Version %d has no entry in VersionNotes", Version)
	}
	if VersionNote(-1) != "" {
		t.Error("an unknown version produced a note")
	}
}
