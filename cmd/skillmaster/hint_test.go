package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// **A command run by a test must not spend this machine's one notice** (ADR 0246): the state file
// would land in the real config directory and the person's own next command would stay quiet —
// `go test` would silently eat it. `init` rather than `TestMain` for the reason rule_report_test.go
// gives: TestMain already lives in this package, and its file is at its size baseline. The two
// tests about the wiring open it again for their own run, with an isolated config directory.
func init() { hintRuns = false }

// ADR 0247's three carriers say the same thing: the rule the agent starts with, the help page it
// reads, and the refusal it gets when the credential is gone. **A refusal without the sentence is
// the path the person was sent down before this version** — "run it again", from the person's side.
func TestTheAgentIsToldToRunTheLoginItself(t *testing.T) {
	if !strings.Contains(string(rule.Block()), "自己运行 `skmr login`") {
		t.Error("the rule block does not tell the agent to run the login itself")
	}
	topic, ok := commandHelp.Topic("rule")
	if !ok {
		t.Fatal("help rule is missing")
	}
	if !strings.Contains(topic.Body, "Agent 自己运行 skmr login") {
		t.Errorf("help rule does not tell the agent to run the login itself:\n%s", topic.Body)
	}
	for _, code := range []string{"unauthenticated", "token_idle", "token_expired", "token_revoked"} {
		sentence, known := refusalMessage(&api.Refusal{Code: code}, false)
		if !known {
			t.Fatalf("%s is not a known refusal", code)
		}
		if !strings.Contains(sentence, agentRunsTheLogin) {
			t.Errorf("the %s refusal does not say the agent can run it: %s", code, sentence)
		}
		// **Not for a key.** A key has no login to run, and sending a key holder to one is the
		// mistake the key sentence's own console address exists to avoid.
		if keySentence, _ := refusalMessage(&api.Refusal{Code: code}, true); strings.Contains(keySentence, agentRunsTheLogin) {
			t.Errorf("the %s key refusal tells the agent to run a login: %s", code, keySentence)
		}
	}
}

func TestHintTextNamesThePathItFound(t *testing.T) {
	missing := hintText(rule.Hint{Kind: rule.HintRecordedMissing, Path: "/h/CLAUDE.md", Agent: "claude-code"})
	// **The recorded line does not claim the rule is gone** — the same record covers an edited file
	// and one its editor ignores, and its first step is the check, not a write that would refuse.
	for _, want := range []string{"/h/CLAUDE.md", "没有可用的守则", "skmr install --check", "install --file /h/CLAUDE.md"} {
		if !strings.Contains(missing, want) {
			t.Errorf("the recorded hint omits %q: %q", want, missing)
		}
	}
	unreadable := hintText(rule.Hint{Kind: rule.HintUnreadableDefault, Path: "/h/skillmaster.mdc", Agent: "cursor"})
	if !strings.Contains(unreadable, "/h/skillmaster.mdc") || !strings.Contains(unreadable, "frontmatter") {
		t.Errorf("the unreadable-default hint does not name the path and the cause: %q", unreadable)
	}
	outdated := hintText(rule.Hint{Kind: rule.HintOutdatedDefault, Path: "/h/AGENTS.md", Agent: "codex"})
	if !strings.Contains(outdated, "/h/AGENTS.md") || !strings.Contains(outdated, "旧版守则") {
		t.Errorf("outdated hint does not name the path: %q", outdated)
	}
	// **The third line names no paths.** A list here would be the default-path table ADR 0242 keeps
	// out of agent output.
	none := hintText(rule.Hint{Kind: rule.HintNothingInstalled})
	for _, forbidden := range []string{".claude", ".codex", ".workbuddy", ".cursor"} {
		if strings.Contains(none, forbidden) {
			t.Errorf("the nothing-installed hint names %q: %q", forbidden, none)
		}
	}
	for _, quiet := range []rule.HintKind{rule.HintNone, rule.HintUnreadable} {
		if line := hintText(rule.Hint{Kind: quiet}); line != "" {
			t.Errorf("kind %d produced a line: %q", quiet, line)
		}
	}
}

// The notice belongs to commands that use the rule, not to the ones that talk about the binary or
// run before a new build is in place.
func TestHintAllowedSkipsTheBinaryAndContractCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"prompt", "print"}, {"update"}} {
		if hintAllowed(args) {
			t.Errorf("%v was allowed to hint", args)
		}
	}
	for _, args := range [][]string{{"skills", "search", "x"}, {"logout"}, {"install", "--file", "/h/C.md", "--yes"}} {
		if !hintAllowed(args) {
			t.Errorf("%v was not allowed to hint", args)
		}
	}
	if hintAllowed(nil) {
		t.Error("an empty command line was allowed to hint")
	}
}

func TestHintRulePrintsOncePerRuleVersion(t *testing.T) {
	dir := isolateConfig(t)
	out := captureStderr(t, func() { hintRule(dir) })
	if !strings.Contains(out, "这台机器还没有守则") {
		t.Fatalf("first run printed %q", out)
	}
	if again := captureStderr(t, func() { hintRule(dir) }); again != "" {
		t.Errorf("the second run printed again: %q", again)
	}
}

func TestHintRuleHonoursDecline(t *testing.T) {
	dir := isolateConfig(t)
	if err := rule.SaveState(dir, rule.State{Schema: 1, Declined: true}); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { hintRule(dir) }); out != "" {
		t.Errorf("a declined machine was hinted: %q", out)
	}
	if rule.HintedVersion(dir) != 0 {
		t.Error("a declined machine's hint state was written")
	}
}

// **A ledger that will not parse says nothing and spends nothing.** Marking the version would keep
// this machine quiet for the whole rule version on the strength of a failed read.
func TestHintRuleSpendsNothingWhenTheLedgerCannotBeRead(t *testing.T) {
	dir := isolateConfig(t)
	if err := os.WriteFile(rule.StatePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { hintRule(dir) }); out != "" {
		t.Errorf("an unreadable ledger produced %q", out)
	}
	if rule.HintedVersion(dir) != 0 {
		t.Error("an unreadable ledger's version was marked")
	}
}

func TestHintRuleSaysNothingWhenTheRuleIsCurrent(t *testing.T) {
	dir := isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	if err := os.WriteFile(target, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rule.SaveState(dir, rule.State{Schema: 1, RuleVersion: rule.Version, Files: []rule.File{
		{Path: target, Agent: "claude-code", RuleVersion: rule.Version},
	}}); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { hintRule(dir) }); out != "" {
		t.Errorf("a current rule was hinted at: %q", out)
	}
	// The version is still recorded, so a future command does not re-scan for nothing.
	if rule.HintedVersion(dir) != rule.Version {
		t.Error("the hint state was not advanced")
	}
}

// The wiring, not the function: a successful command carries the line, the next one does not, and
// the state is where the run left it.
func TestRunPrintsTheRuleNoticeOnce(t *testing.T) {
	dir := isolateConfig(t)
	hintRuns = true
	t.Cleanup(func() { hintRuns = false })
	first := captureStderr(t, func() {
		if err := run(context.Background(), []string{"logout"}); err != nil {
			t.Fatalf("logout: %v", err)
		}
	})
	if !strings.Contains(first, "这台机器还没有守则") {
		t.Fatalf("the first command printed %q", first)
	}
	if second := captureStderr(t, func() { _ = run(context.Background(), []string{"logout"}) }); second != "" {
		t.Errorf("the second command printed again: %q", second)
	}
	if rule.HintedVersion(dir) != rule.Version {
		t.Errorf("hinted version = %d, want %d", rule.HintedVersion(dir), rule.Version)
	}
}

// A refused command sends and says nothing (ADR 0246 rides the same success), and the notice stays
// unspent for the next command that does succeed.
//
// **`logout extra`, not `install` with no file**: an `install` without `--yes` is read-only and is
// refused before the notice is ever considered, so it would pass with the success guard removed.
// This one fails inside the dispatch, after the deferred branch is armed.
func TestRunLeavesTheNoticeUnspentWhenTheCommandFails(t *testing.T) {
	dir := isolateConfig(t)
	hintRuns = true
	t.Cleanup(func() { hintRuns = false })
	out := captureStderr(t, func() {
		if err := run(context.Background(), []string{"logout", "extra"}); err == nil {
			t.Fatal("logout accepted an argument")
		}
	})
	if out != "" {
		t.Errorf("a failed command printed %q", out)
	}
	if rule.HintedVersion(dir) != 0 {
		t.Error("a failed command marked the version")
	}
}
