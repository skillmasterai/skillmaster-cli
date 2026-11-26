package main

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/auth"
	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/credentials"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
	"github.com/skillmasterai/skillmaster-cli/internal/version"
)

// The rule-coverage report (v94, ADR 0242; detection-only since 1.10.1, ADR 0249).
//
// **Best effort, at most once a day, and never in the way.** A missing credential or a failed send
// leaves the command exactly as it was; `SKILLMASTER_REPORT=0` turns it off. What travels is the
// fields of api.RuleReport — no path, no file name, no rule text.

const reportEnv = "SKILLMASTER_REPORT"

// reportRule scans the known default locations and sends one row per detected client.
//
// **Nothing is written before the scan and nothing is remembered about it.** What the report says
// is read on the spot; the only local state is the throttle in `rule-state.json` (ADR 0249).
func reportRule(dir string) {
	if os.Getenv(reportEnv) == "0" {
		return
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil {
		// A read error (permissions, a directory) has no known throttle and no known refusal.
		// Reporting anyway could send on every command, and a defaulted `declined` would be a
		// fact nobody stated. A parse error is already a fresh state.
		return
	}
	now := time.Now()
	if !rule.ReportDue(st, now) {
		return
	}
	client, ok := reportingClient()
	if !ok {
		return
	}
	// **No home directory, no default-path answer.** Passing "" would make the scans relative to the
	// current directory, where a repository may happen to have the same file.
	home, homeErr := os.UserHomeDir()
	var detections []rule.Detection
	if homeErr == nil && home != "" {
		detections = rule.Detect(home)
	}
	// **The attempt is recorded before it is sent.** A failed send must not turn "at most once a
	// day" into "once per command": a dead endpoint would otherwise add three seconds to every run.
	rule.MarkReported(dir, now)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// `rule_version` answers for the client as a whole — the rule this binary carries — because
	// 1.10.1 has no per-file record to read a version off.
	base := api.RuleReport{
		RuleVersion: rule.Version,
		Declined:    st.Declined,
		CLIVersion:  version.String(),
		OS:          reportOS(),
	}
	if len(detections) == 0 {
		// **A machine with no known file still has something worth saying:** it has none of the
		// clients this build detects. "unknown" says exactly that and names no path.
		base.Agent = "unknown"
		_ = client.ReportRuleInstall(ctx, base)
		return
	}
	for _, detection := range detections {
		report := base
		report.Agent = detection.Agent
		report.DefaultRulePresent = detection.Present
		// **One bad row must not swallow the others.** Nothing is printed: telemetry is noise in a
		// command that succeeded, and the next day's attempt is the retry.
		_ = client.ReportRuleInstall(ctx, report)
	}
}

// reportingClient is the client without the login path and without the keychain.
//
// **Telemetry must never open a browser or raise a keychain dialog.** A command that does not touch
// the network should not become a reason for macOS to ask the person for permission to read a
// credential. The environment key and the credential file are both safe to read here; a
// keychain-only login simply means no report.
func reportingClient() (api.Client, bool) {
	server := config.Server()
	if key := os.Getenv(config.ApiKeyEnv); key != "" {
		return api.Client{BaseURL: server, Token: key}, true
	}
	write, older, err := config.CredentialPaths(server)
	if err != nil {
		return api.Client{}, false
	}
	stored, err := auth.AccessToken(credentials.OpenFile(credentials.Location{Path: write, Older: older}))
	if err != nil {
		return api.Client{}, false
	}
	return api.Client{BaseURL: server, Token: stored.AccessToken}, true
}

func reportOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}
