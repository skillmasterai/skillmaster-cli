package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// **The line that answers 「我换了一个 AI，还要再写吗」 now reads the rule install state** (v94,
// ADR 0242). `prompt.json` is frozen history; what counts is which agents `skmr install --file` has
// actually written, and that is `install.json`.
func TestStartupPrintsTheRecordedAIsOnStderr(t *testing.T) {
	const body = "# SkillMaster 入门\n\n一条链，不是菜单。\n"
	server := startupServer(t, body)
	t.Setenv(config.ServerEnv, server.URL)
	t.Setenv(config.ApiKeyEnv, "skm_livekey123456")
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	writeFile(t, target, string(rule.Block()))
	if err := rule.SaveState(dir, rule.State{Schema: 1, RuleVersion: rule.Version, Files: []rule.File{{
		Path: target, Agent: "claude",
	}}}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, status := runCaptured(t, "startup")
	if status != 0 {
		t.Fatalf("status = %d, stderr=%s", status, stderr)
	}
	if !strings.HasSuffix(stdout, body) {
		t.Errorf("stdout does not end with the journey body:\n%s", stdout)
	}
	if !strings.Contains(stderr, "已经写入守则的 Agent：claude") {
		t.Errorf("stderr does not name the recorded AI:\n%s", stderr)
	}
}

// **`startup` takes no arguments any more** (ADR 0111). Each of the three flags it used to accept
// is now an unknown argument, and none of them may be quietly ignored: a person who typed
// `--plan` is asking a question that no longer has an answer, and silence would answer it wrongly.
func TestStartupTakesNoArguments(t *testing.T) {
	isolateConfig(t)
	for _, args := range [][]string{
		{"startup", "--plan"},
		{"startup", "--agent", "codex"},
		{"startup", "--file", filepath.Join(os.Getenv("HOME"), "AGENTS.md")},
	} {
		stdout, stderr, status := runCaptured(t, args...)
		if status != 2 {
			t.Errorf("%v status = %d, want 2 (usage)", args, status)
		}
		if !strings.Contains(stdout+stderr, args[1]) {
			t.Errorf("%v does not name the argument it refused:\n%s%s", args, stdout, stderr)
		}
	}
}
