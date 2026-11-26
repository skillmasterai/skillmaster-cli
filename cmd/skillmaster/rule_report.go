package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/auth"
	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/credentials"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
	"github.com/skillmasterai/skillmaster-cli/internal/version"
)

// The rule-coverage report (v94, ADR 0242).
//
// **Best effort, at most once a day except an install write, and never in the way.** A missing credential or a failed send
// leaves the command exactly as it was; `SKILLMASTER_REPORT=0` turns it off. What travels is seven
// booleans and enums — see api.RuleReport — and never a path or any file content.

const reportInterval = 24 * time.Hour

type reportState struct {
	SentAt string `json:"sent_at"`
}

func reportPath(dir string) string { return filepath.Join(dir, "rule-report.json") }

func reportDue(dir string) bool {
	blob, err := os.ReadFile(reportPath(dir))
	if err != nil {
		return true
	}
	var state reportState
	if json.Unmarshal(blob, &state) != nil {
		return true
	}
	sent, err := time.Parse(time.RFC3339, state.SentAt)
	if err != nil {
		return true
	}
	return time.Since(sent) >= reportInterval
}

func markReported(dir string) {
	blob, _ := json.Marshal(reportState{SentAt: time.Now().UTC().Format(time.RFC3339)})
	// **Atomic like the state file**: a half-written throttle file would make the next command send
	// again, turning "once a day" into "once per command" on a machine that loses power.
	temp, err := os.CreateTemp(dir, ".rule-report-*.json")
	if err != nil {
		return
	}
	if _, err := temp.Write(blob); err != nil {
		temp.Close()
		return
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Rename(temp.Name(), reportPath(dir))
}

// reportRule sends one row per recorded agent. `force` is `install`'s path: a person who just ran
// the writing command should not have to wait a day for the coverage to show up.
func reportRule(dir string, force bool) {
	if os.Getenv(reportEnv) == "0" {
		return
	}
	if !force && !reportDue(dir) {
		return
	}
	client, ok := reportingClient()
	if !ok {
		return
	}
	st, known, err := rule.LoadState(dir)
	if err != nil {
		return
	}
	// **No home directory, no default-path answer.** Passing "" would make the known-file globs
	// relative to the current directory, where a repository may happen to have the same file.
	home, homeErr := os.UserHomeDir()
	defaultPresent := false
	if homeErr == nil && home != "" {
		// **"No known file here" travels as false, and that is the only value the wire takes.**
		// `default_rule_present` is a required boolean; the "nothing to look at" case is kept apart
		// by the row instead — a machine with no record reports agent "unknown" with a null
		// `agent_rule_present`, so the server never reads this false as "checked and absent".
		defaultPresent, _ = rule.DefaultPresent(home)
	}
	osFamily := reportOS()
	rows := reportRows(st)
	// **The attempt is recorded before it is sent.** A failed send must not turn "at most once a
	// day" into "once per command": a dead endpoint would otherwise add three seconds to every run.
	markReported(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The version that describes what is on disk, not the binary's wish: `install.json` records the
	// rule last written. A machine with **no record** is reporting the rule this build carries; a
	// record still at 0 came from the retired store and says so (0 = written before versioning).
	ruleVersion := managedVersion(st)
	if !known || len(st.Files) == 0 {
		// No record, or a state that only remembers a refusal: nothing was ever written here, so
		// the honest answer is the version this build carries — not 0, which means "old rule".
		ruleVersion = rule.Version
	}
	for _, row := range rows {
		report := api.RuleReport{
			Agent:              row.agent,
			DefaultRulePresent: defaultPresent,
			AgentRulePresent:   row.present,
			RuleVersion:        ruleVersion,
			Declined:           st.Declined,
			CLIVersion:         version.String(),
			OS:                 osFamily,
		}
		// One bad row must not swallow the others. Nothing is printed: telemetry is noise in a
		// command that succeeded, and the next day's attempt is the retry.
		_ = client.ReportRuleInstall(ctx, report)
	}
}

const reportEnv = "SKILLMASTER_REPORT"

type reportRow struct {
	agent   string
	present *bool
}

// reportRows turns the install records into reports. A machine with no record still has something
// worth saying — whether the rule sits at a known default path — so it reports once as `unknown`
// with a nil agent answer, which the server stores as NULL rather than false.
func reportRows(st rule.State) []reportRow {
	if len(st.Files) == 0 {
		return []reportRow{{agent: "unknown"}}
	}
	// **One row per agent.** The server's key is (account, agent, day); two files labelled with the
	// same agent would overwrite each other, so their answers are folded into one: the agent-level
	// question is "does this agent see the rule anywhere", and any is the honest answer.
	order := make([]string, 0, len(st.Files))
	answers := map[string]bool{}
	for _, f := range st.Files {
		// **A record this command never manages is not coverage.** It is a repository file or a
		// symlink; counting it would report "installed" for a machine with no user-level rule — and
		// the symlink case has to be judged now, not at the next rule version: `Check` calls that
		// record 「不管」 the moment the path changes, and the two answers must match.
		if f.Unmanaged || !rule.PathIsManageable(f.Path) {
			continue
		}
		// Records written before v94 carry whatever the person typed, and the server accepts only a
		// slug; normalising here covers records the migration never touched.
		agent := rule.NormalizeAgent(f.Agent)
		if _, ok := answers[agent]; !ok {
			order = append(order, agent)
		}
		answers[agent] = answers[agent] || rule.FileHasRule(f.Path)
	}
	rows := make([]reportRow, 0, len(order))
	for _, agent := range order {
		present := answers[agent]
		rows = append(rows, reportRow{agent: agent, present: &present})
	}
	if len(rows) == 0 {
		// Every record was out of scope; the machine is in the same position as one with no record.
		return []reportRow{{agent: "unknown"}}
	}
	return rows
}

// managedVersion is the oldest rule version among the records this command manages.
//
// **Out-of-scope records do not answer for the machine.** A settled project-level record would
// otherwise report the current version on a machine whose only rule lives in a repository.
func managedVersion(st rule.State) int {
	oldest := -1
	for _, f := range st.Files {
		if f.Unmanaged {
			continue
		}
		// **0 is a real answer** (a record seeded from the retired store, never migrated), so the
		// "no managed record" case is tracked separately.
		if oldest < 0 || f.RuleVersion < oldest {
			oldest = f.RuleVersion
		}
	}
	if oldest < 0 {
		return rule.Version
	}
	return oldest
}

// reportingClient is the client without the login path and without the keychain.
//
// **Telemetry must never open a browser or raise a keychain dialog.** A command that does not touch
// the network (an install, a local check) should not become a reason for macOS to ask the person for
// permission to read a credential. The environment key and the credential file are both safe to read
// here; a keychain-only login simply means no report.
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
