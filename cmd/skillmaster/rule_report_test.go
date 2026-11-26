package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// **No test sends the coverage report unless it is about the report.** Every other case's fake
// server rejects unknown paths, and a telemetry request after a successful command would look like
// the command making a request it promised not to make. `init` rather than `TestMain`: TestMain
// already lives in this package, and its file is at its size baseline.
func init() {
	if os.Getenv(reportEnv) == "" {
		os.Setenv(reportEnv, "0")
	}
}

func TestReportRowsSayUnknownWhenNothingWasInstalled(t *testing.T) {
	rows := reportRows(rule.State{})
	if len(rows) != 1 || rows[0].agent != "unknown" {
		t.Fatalf("rows = %+v, want one unknown row", rows)
	}
	if rows[0].present != nil {
		t.Error("agent_rule_present must be nil when there is no record")
	}
}

func TestReportRowsReadTheRuleFromEachRecordedFile(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "a.md")
	absent := filepath.Join(dir, "b.md")
	if err := os.WriteFile(present, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := reportRows(rule.State{Files: []rule.File{
		{Path: present, Agent: "claude-code"},
		{Path: absent, Agent: "codex"},
	}})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].present == nil || !*rows[0].present {
		t.Error("the file that still has the rule reported otherwise")
	}
	if rows[1].present == nil || *rows[1].present {
		t.Error("a missing file reported as present")
	}
}

func TestReportRowsFoldTwoFilesOfOneAgentIntoOneRow(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "a.md")
	absent := filepath.Join(dir, "b.md")
	if err := os.WriteFile(present, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := reportRows(rule.State{Files: []rule.File{
		{Path: present, Agent: "claude-code"},
		{Path: absent, Agent: "claude-code"},
	}})
	if len(rows) != 1 || rows[0].present == nil || !*rows[0].present {
		t.Fatalf("rows = %+v, want one present row", rows)
	}
}

func TestReportSendsOnlyTheSevenFields(t *testing.T) {
	dir := isolateConfig(t)
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cli/rule-reports" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv(config.ServerEnv, server.URL)
	t.Setenv(config.ApiKeyEnv, "skm_livekey123456")
	t.Setenv(reportEnv, "1")

	if err := rule.SaveState(dir, rule.State{Schema: 1, RuleVersion: rule.Version,
		Files: []rule.File{{Path: filepath.Join(dir, "CLAUDE.md"), Agent: "claude-code"}}}); err != nil {
		t.Fatal(err)
	}
	reportRule(dir, true)

	if body == nil {
		t.Fatal("no report was sent")
	}
	want := map[string]bool{"agent": true, "default_rule_present": true,
		"agent_rule_present": true, "rule_version": true, "declined": true,
		"cli_version": true, "os": true}
	for key := range body {
		if !want[key] {
			t.Errorf("the report carries an unexpected field %q", key)
		}
	}
	for key := range want {
		if _, ok := body[key]; !ok {
			t.Errorf("the report is missing %q", key)
		}
	}
}

// **A machine that only remembers a refusal has no rule to report.** Sending 0 would say "an old
// rule is installed" about a machine that never installed one, and skew the old-rule share.
func TestReportSendsTheBinaryVersionWhenOnlyARefusalIsRecorded(t *testing.T) {
	dir := isolateConfig(t)
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv(config.ServerEnv, server.URL)
	t.Setenv(config.ApiKeyEnv, "skm_livekey123456")
	t.Setenv(reportEnv, "1")
	if err := rule.SaveState(dir, rule.State{Schema: 1, Declined: true}); err != nil {
		t.Fatal(err)
	}
	reportRule(dir, true)
	if body == nil {
		t.Fatal("no report was sent")
	}
	if got := body["rule_version"]; got != float64(rule.Version) {
		t.Errorf("rule_version = %v, want %d", got, rule.Version)
	}
}

// **A record this command never manages is not coverage.** Reporting it would claim a user-level
// rule on a machine whose only rule sits in a repository.
func TestReportSkipsUnmanagedRecords(t *testing.T) {
	if rows := reportRows(rule.State{Files: []rule.File{
		{Path: "/nowhere", Agent: "codex", Unmanaged: true},
	}}); len(rows) != 1 || rows[0].agent != "unknown" || rows[0].present != nil {
		t.Errorf("rows = %+v, want one unknown row with no answer", rows)
	}
	managed := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(managed, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := reportRows(rule.State{Files: []rule.File{
		{Path: "/nowhere", Agent: "codex", Unmanaged: true},
		{Path: managed, Agent: "claude-code"},
	}})
	if len(rows) != 1 || rows[0].agent != "claude-code" {
		t.Errorf("rows = %+v, want only the managed agent", rows)
	}
}

// **A record that became a link is not coverage**, judged the same moment `Check` judges it.
func TestReportSkipsARecordThatBecameALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	rows := reportRows(rule.State{Files: []rule.File{{Path: link, Agent: "claude-code"}}})
	if len(rows) != 1 || rows[0].agent != "unknown" || rows[0].present != nil {
		t.Errorf("rows = %+v, want one unknown row", rows)
	}
}

func TestManagedVersionComesFromManagedRecords(t *testing.T) {
	if got := managedVersion(rule.State{Files: []rule.File{{Path: "/nowhere", Unmanaged: true}}}); got != rule.Version {
		t.Errorf("version = %d, want the binary's %d", got, rule.Version)
	}
	if got := managedVersion(rule.State{Files: []rule.File{
		{Path: "/a", RuleVersion: 0}, {Path: "/b", RuleVersion: rule.Version},
	}}); got != 0 {
		t.Errorf("version = %d, want the oldest managed 0", got)
	}
	if got := managedVersion(rule.State{Files: []rule.File{
		{Path: "/a", RuleVersion: 0, Unmanaged: true}, {Path: "/b", RuleVersion: rule.Version},
	}}); got != rule.Version {
		t.Errorf("version = %d, want the managed %d", got, rule.Version)
	}
}

func TestReportIsOffWhenDisabled(t *testing.T) {
	dir := isolateConfig(t)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()
	t.Setenv(config.ServerEnv, server.URL)
	t.Setenv(config.ApiKeyEnv, "skm_livekey123456")
	t.Setenv(reportEnv, "0")

	reportRule(dir, true)
	if called {
		t.Error("a report was sent with SKILLMASTER_REPORT=0")
	}
}

func TestReportIsThrottledForADay(t *testing.T) {
	dir := isolateConfig(t)
	if reportDue(dir) != true {
		t.Error("a fresh directory is not due")
	}
	markReported(dir)
	if reportDue(dir) {
		t.Error("a report sent just now is due again")
	}
}
