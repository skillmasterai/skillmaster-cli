package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

// reportServer records every request body and answers 204.
type reportServer struct {
	mu       sync.Mutex
	bodies   []map[string]any
	status   int
	requests int
}

func newReportServer(t *testing.T) *reportServer {
	t.Helper()
	recorder := &reportServer{status: http.StatusNoContent}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cli/rule-reports" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, body)
		recorder.requests++
		status := recorder.status
		recorder.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	t.Setenv(config.ServerEnv, server.URL)
	t.Setenv(config.ApiKeyEnv, "skm_livekey123456")
	t.Setenv(reportEnv, "1")
	return recorder
}

// isolatedMachine is an empty temp home and the config directory inside it: no client files until a
// test writes them, and none of this machine's own.
func isolatedMachine(t *testing.T) (home, dir string) {
	t.Helper()
	dir = isolateConfig(t)
	// A developer's shell may point these at real directories; the scan must see only the temp home.
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	return os.Getenv("HOME"), dir
}

// writeInstructionFile puts content at a path under home, creating the parents.
func writeInstructionFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReportSendsOneRowForEachDetectedClient: the row says only "this client has a file here, and
// whether that file carries the current rule" (ADR 0249 §决定 1).
func TestReportSendsOneRowForEachDetectedClient(t *testing.T) {
	home, dir := isolatedMachine(t)
	writeInstructionFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), rule.Block())
	writeInstructionFile(t, filepath.Join(home, ".codex", "AGENTS.md"), []byte("别的说明\n"))
	recorder := newReportServer(t)

	reportRule(dir)

	if recorder.requests != 2 {
		t.Fatalf("requests = %d, want 2 (one per detected client)", recorder.requests)
	}
	answers := map[string]bool{}
	for _, body := range recorder.bodies {
		agent, _ := body["agent"].(string)
		present, _ := body["default_rule_present"].(bool)
		answers[agent] = present
	}
	if !answers["claude-code"] {
		t.Errorf("claude-code reported not present: %v", answers)
	}
	if answers["codex"] {
		t.Errorf("codex reported present: %v", answers)
	}
}

// **An unreadable state file stops the report instead of guessing.** A missing throttle would send
// on every command, and a defaulted `declined` would be a fact nobody stated.
func TestReportStaysSilentWhenTheStateCannotBeRead(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)
	if err := os.Mkdir(filepath.Join(dir, rule.StateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	reportRule(dir)
	if recorder.requests != 0 {
		t.Errorf("requests = %d, want 0", recorder.requests)
	}
}

// **A machine with no known file reports once as `unknown`.** Nothing is claimed about a client
// that is not there; the label says the report covers none this build knows.
func TestReportSaysUnknownWhenNoClientIsDetected(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)

	reportRule(dir)

	if recorder.requests != 1 {
		t.Fatalf("requests = %d, want 1", recorder.requests)
	}
	body := recorder.bodies[0]
	if body["agent"] != "unknown" {
		t.Errorf("agent = %v, want unknown", body["agent"])
	}
	if present, ok := body["default_rule_present"].(bool); !ok || present {
		t.Errorf("default_rule_present = %v, want false", body["default_rule_present"])
	}
}

// TestReportSendsOnlyTheFieldsTheServerKnows: 1.10.1 adds no column (ADR 0249 §决定 5), so a field the
// 1.x server never saw would be a report it refuses.
func TestReportSendsOnlyTheFieldsTheServerKnows(t *testing.T) {
	home, dir := isolatedMachine(t)
	writeInstructionFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), rule.Block())
	recorder := newReportServer(t)

	reportRule(dir)

	if recorder.requests != 1 {
		t.Fatalf("requests = %d, want 1", recorder.requests)
	}
	body := recorder.bodies[0]
	want := map[string]bool{"agent": true, "default_rule_present": true, "rule_version": true,
		"declined": true, "cli_version": true, "os": true}
	for key := range body {
		if !want[key] {
			t.Errorf("the report carries a field the server does not know: %q", key)
		}
	}
	for key := range want {
		if _, ok := body[key]; !ok {
			t.Errorf("the report is missing %q", key)
		}
	}
	if got := body["rule_version"]; got != float64(rule.Version) {
		t.Errorf("rule_version = %v, want %d", got, rule.Version)
	}
}

// **At most once a day** (ADR 0249 §决定 1): the throttle is `reported_at`, and a send that just
// happened keeps the next command quiet.
func TestReportIsThrottledForADay(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)
	if err := rule.SaveLocalState(dir, rule.LocalState{Schema: 2,
		ReportedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	reportRule(dir)

	if recorder.requests != 0 {
		t.Fatalf("requests = %d, want 0 inside the day", recorder.requests)
	}
}

// **The attempt is recorded before the request** (v94, kept): a dead endpoint must not turn "once a
// day" into "once per command".
func TestReportMarksTheAttemptBeforeSending(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)
	recorder.mu.Lock()
	recorder.status = http.StatusInternalServerError
	recorder.mu.Unlock()

	reportRule(dir)

	if recorder.requests != 1 {
		t.Fatalf("requests = %d, want 1", recorder.requests)
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil || st.ReportedAt == "" || rule.ReportDue(st, time.Now()) {
		t.Errorf("reported_at = %q (err %v), want a fresh stamp", st.ReportedAt, err)
	}
}

// A refusal travels with every row, so the coverage table can tell "not installed" from "asked and
// told no".
func TestReportCarriesTheRefusal(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)
	if err := rule.SetDeclined(dir, true); err != nil {
		t.Fatal(err)
	}

	reportRule(dir)

	if recorder.requests != 1 {
		t.Fatalf("requests = %d, want 1", recorder.requests)
	}
	if declined, _ := recorder.bodies[0]["declined"].(bool); !declined {
		t.Errorf("declined = %v, want true", recorder.bodies[0]["declined"])
	}
}

func TestReportHonoursTheOffSwitch(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)
	t.Setenv(reportEnv, "0")

	reportRule(dir)

	if recorder.requests != 0 {
		t.Fatalf("requests = %d, want 0", recorder.requests)
	}
}

// **A corrupt state file is treated as a fresh machine.** Saving in place can leave a torn file;
// one extra report repairs the throttle rather than silencing it forever.
func TestReportTreatsACorruptStateAsFresh(t *testing.T) {
	_, dir := isolatedMachine(t)
	recorder := newReportServer(t)
	if err := os.WriteFile(rule.LocalStatePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	reportRule(dir)

	if recorder.requests != 1 {
		t.Fatalf("requests = %d, want 1", recorder.requests)
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.ReportedAt == "" {
		t.Error("the save did not repair reported_at")
	}
}
