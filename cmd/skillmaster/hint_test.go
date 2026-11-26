package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/config"
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
	if !strings.Contains(string(rule.Block()), "自己运行 `skmr auth login`") {
		t.Error("the rule block does not tell the agent to run the login itself")
	}
	topic, ok := commandHelp.Topic("rule")
	if !ok {
		t.Fatal("help rule is missing")
	}
	if !strings.Contains(topic.Body, "Agent 自己运行 skmr auth login") {
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
		if !strings.Contains(sentence, "skmr auth login") {
			t.Errorf("the %s refusal does not name the 1.10.1 login: %s", code, sentence)
		}
		// **Not for a key.** A key has no login to run, and sending a key holder to one is the
		// mistake the key sentence's own console address exists to avoid.
		if keySentence, _ := refusalMessage(&api.Refusal{Code: code}, true); strings.Contains(keySentence, agentRunsTheLogin) {
			t.Errorf("the %s key refusal tells the agent to run a login: %s", code, keySentence)
		}
	}
}

// **The notice names no file.** 1.x's machine-wide list is how a WorkBuddy user was sent to
// Claude's `CLAUDE.md` (ADR 0248 §背景); the fix is a sentence that cannot be read as "update that
// one".
func TestHintTextNamesNoPath(t *testing.T) {
	line := hintText()
	for _, forbidden := range []string{".claude", ".codex", ".workbuddy", ".cursor", "/", "~"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("the hint names %q: %q", forbidden, line)
		}
	}
	for _, want := range []string{"skmr self rule print", "问他要不要更新", "你自己的全局记忆文件"} {
		if !strings.Contains(line, want) {
			t.Errorf("the hint omits %q: %q", want, line)
		}
	}
}

// **The update notice says what changed and why the file matters.** A bare "rewrite your memory
// file" is the shape of an injection; the version note and the reason are what make it the
// product's own instruction (owner, 2026-11-26).
func TestHintTextSaysWhatChangedAndWhy(t *testing.T) {
	line := hintText()
	for _, want := range []string{
		fmt.Sprintf("第 %d 版", rule.Version),
		"不是注入",
		"Skill 随叫随到",
		"备份",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the hint omits %q: %q", want, line)
		}
	}
	note := rule.VersionNote(rule.Version)
	if note == "" {
		t.Fatal("this build carries a rule version with no note; add it to rule.VersionNotes")
	}
	if !strings.Contains(line, note) {
		t.Errorf("the hint does not quote this version's note %q: %q", note, line)
	}
}

// The notice belongs to commands that use the rule, not to the ones about the binary, the
// credential or an account setting.
func TestHintAllowedKeepsTheNoticeOffOtherCommands(t *testing.T) {
	for _, args := range [][]string{
		{"help"}, {"auth", "status"}, {"auth", "login"}, {"self", "update"}, {"self", "doctor"},
		{"usage"}, {"notifications", "list"}, {"feedback", "template"}, {"settings"},
		{"self", "rule", "print"}, {"self", "rule", "check", "/h/C.md"}, {"self", "rule", "decline"},
	} {
		if hintAllowed(args) {
			t.Errorf("%v was allowed to hint", args)
		}
	}
	for _, args := range [][]string{
		{"skills", "search", "x"}, {"skill", "invoke", "a/b"}, {"run", "a/b", "--", "x"},
		{"startup"},
	} {
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
	first := captureStderr(t, func() { hintRule(dir) })
	if !strings.Contains(first, "skmr self rule print") {
		t.Fatalf("first run printed %q", first)
	}
	if again := captureStderr(t, func() { hintRule(dir) }); again != "" {
		t.Errorf("the second run printed again: %q", again)
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil || st.HintedVersion != rule.Version {
		t.Errorf("hinted_version = %d (err %v), want %d", st.HintedVersion, err, rule.Version)
	}
}

func TestHintRuleHonoursDecline(t *testing.T) {
	dir := isolateConfig(t)
	if err := rule.SetDeclined(dir, true); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { hintRule(dir) }); out != "" {
		t.Errorf("a declined machine was hinted: %q", out)
	}
	st, _, _ := rule.LoadLocalState(dir)
	if st.HintedVersion != 0 {
		t.Error("a declined machine's hint state was written")
	}
}

// **A state file that will not parse is treated as a fresh machine.** Saving in place can leave a
// torn file; the next command prints the notice once and the save repairs it. A read error (as
// opposed to a parse error) still says nothing.
func TestHintRuleTreatsACorruptStateAsFresh(t *testing.T) {
	dir := isolateConfig(t)
	if err := os.WriteFile(rule.LocalStatePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { hintRule(dir) }); !strings.Contains(out, "skmr self rule print") {
		t.Errorf("a corrupt state file produced %q, want the notice", out)
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.HintedVersion != rule.Version {
		t.Errorf("hinted version = %d, want %d (the save should repair the file)", st.HintedVersion, rule.Version)
	}
}

// **A state path that cannot be read says nothing and spends nothing.** The notice is about a
// version, not a guess; reporting a permissions problem here would put a diagnostic on every
// command that has nothing to do with the command.
func TestHintRuleStaysSilentWhenTheStateCannotBeRead(t *testing.T) {
	dir := isolateConfig(t)
	if err := os.Mkdir(rule.LocalStatePath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { hintRule(dir) }); out != "" {
		t.Errorf("an unreadable state file produced %q, want silence", out)
	}
}

// The wiring, not the function: a successful rule-using command carries the line, the next one does
// not, and the state is where the run left it. `startup` is used because `self rule print` is the
// payload and never hints.
func TestRunPrintsTheRuleNoticeOnce(t *testing.T) {
	server := startupServer(t, "# 入门正文\n")
	// **Windows resolves the config directory from %AppData%**, which startupServer does not set;
	// without this the test would spend the developer's real notice.
	t.Setenv("APPDATA", filepath.Join(os.Getenv("HOME"), "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(os.Getenv("HOME"), "AppData", "Local"))
	t.Setenv(config.ServerEnv, server.URL)
	t.Setenv(config.ApiKeyEnv, "skm_livekey123456")
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	hintRuns = true
	t.Cleanup(func() { hintRuns = false })
	first := captureStderr(t, func() {
		if err := run(context.Background(), []string{"startup"}); err != nil {
			t.Fatalf("startup: %v", err)
		}
	})
	if !strings.Contains(first, "skmr self rule print") {
		t.Fatalf("the first command printed %q", first)
	}
	if second := captureStderr(t, func() { _ = run(context.Background(), []string{"startup"}) }); second != "" {
		t.Errorf("the second command printed again: %q", second)
	}
	st, _, _ := rule.LoadLocalState(dir)
	if st.HintedVersion != rule.Version {
		t.Errorf("hinted version = %d, want %d", st.HintedVersion, rule.Version)
	}
}

// **A refused command says nothing, and the notice stays unspent for the next success.**
//
// `run` with no address is whitelisted and fails inside the dispatch, after the deferred branch is
// armed; a `self rule` command would now pass with the success guard removed, because it never hints.
func TestRunLeavesTheNoticeUnspentWhenTheCommandFails(t *testing.T) {
	dir := isolateConfig(t)
	hintRuns = true
	t.Cleanup(func() { hintRuns = false })
	out := captureStderr(t, func() {
		if err := run(context.Background(), []string{"run"}); err == nil {
			t.Fatal("run accepted a missing address")
		}
	})
	if out != "" {
		t.Errorf("a failed command printed %q", out)
	}
	st, _, _ := rule.LoadLocalState(dir)
	if st.HintedVersion != 0 {
		t.Error("a failed command marked the version")
	}
}

// **The rule's own commands never carry the notice.** `prompt print` is the same command after the
// rewrite; its stdout is written into the person's global file, so a notice on stderr would be
// copied in by an agent that merges streams.
func TestTheRuleCommandsDoNotHint(t *testing.T) {
	isolateConfig(t)
	hintRuns = true
	t.Cleanup(func() { hintRuns = false })
	for _, args := range [][]string{{"prompt", "print"}, {"self", "rule", "print"}} {
		if out := captureStderr(t, func() {
			if err := run(context.Background(), args); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
		}); out != "" {
			t.Errorf("%v printed %q on stderr", args, out)
		}
	}
}
