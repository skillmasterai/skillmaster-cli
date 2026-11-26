package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/prompt"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// `install` is deprecated in 1.10.1, and every `prompt` spelling but `print` moved (ADR 0248 §3).
// They still answer — a person or an old script that types one deserves to know where the work
// went — and they write nothing.
func TestTheDeprecatedRuleCommandsExplainThemselves(t *testing.T) {
	dir := isolateConfig(t)
	file := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	if err := os.WriteFile(file, []byte("前文\n"+string(prompt.Block())+"后文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)

	cases := map[string][]string{
		"install":          {"install", "--file", file, "--yes"},
		"install --help":   {"install", "--help"},
		"prompt":           {"prompt"},
		"prompt status":    {"prompt", "status"},
		"prompt update":    {"prompt", "update", "--yes"},
		"prompt install":   {"prompt", "install", "--file", file, "--yes"},
		"prompt uninstall": {"prompt", "uninstall", "--file", file, "--yes"},
	}
	for name, args := range cases {
		stdout, stderr, status := runCaptured(t, args...)
		if status != 2 {
			t.Errorf("%s status = %d, want 2", name, status)
		}
		// **The refusal is on stderr, and stdout stays empty**: stdout is where a result goes, and
		// the rule group's only result is the block from `self rule print`.
		if stdout != "" {
			t.Errorf("%s wrote to stdout: %q", name, stdout)
		}
		if !strings.Contains(stderr, "skmr self rule") {
			t.Errorf("%s does not name the successor:\n%s", name, stderr)
		}
		if strings.HasPrefix(name, "install") && !strings.Contains(stderr, "已废弃") {
			t.Errorf("%s does not say it is deprecated:\n%s", name, stderr)
		}
	}

	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Error("a deprecated command changed the file")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("a deprecated install created a file")
	}
	// **Nothing new appears anywhere.** 1.x wrote `install.json`; a deprecated command that quietly
	// started a record file again would be the same regression in a quieter place.
	for _, name := range []string{"install.json", "prompt.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("a deprecated command wrote %s", name)
		}
	}
}

// The one old spelling that survives, byte for byte: `prompt print` is the same output as
// `self rule print`, from the same embedded file.
func TestPromptPrintStillPrintsTheBlock(t *testing.T) {
	isolateConfig(t)
	fresh := captureStdout(t, func() {
		if err := run(context.Background(), []string{"self", "rule", "print"}); err != nil {
			t.Fatalf("self rule print: %v", err)
		}
	})
	legacy := captureStdout(t, func() {
		if err := run(context.Background(), []string{"prompt", "print"}); err != nil {
			t.Fatalf("prompt print: %v", err)
		}
	})
	if legacy != fresh || fresh != string(prompt.Block()) {
		t.Errorf("prompt print = %q, self rule print = %q, block = %q", legacy, fresh, prompt.Block())
	}
}

// **A file inside a Git repository is project-level.** The published startup skill and the install
// page tell the agent to stop when they see 不管; that answer has to exist.
func TestSelfRuleCheckRefusesAProjectLevelFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(prompt.Block()), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, status := runCaptured(t, "self", "rule", "check", path)
	if status != 1 || !strings.HasPrefix(stdout, "不管") {
		t.Errorf("status = %d, output = %q; want 不管/1", status, stdout)
	}
}

// The alias is the same command, help included (test-plan T2).
func TestPromptPrintHelpIsTheNewHelp(t *testing.T) {
	isolateConfig(t)
	stdout, _, status := runCaptured(t, "prompt", "print", "--help")
	if status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if !strings.Contains(stdout, "用法：skmr self rule print") {
		t.Errorf("help = %q, want self rule print's page", stdout)
	}
}

// `self rule check` is the agent's read-back: 0 only for the current rule, 1 for every other
// answer, 2 for a usage error (ADR 0249 §后果).
func TestSelfRuleCheckAnswersWithTheState(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.md")
	if err := os.WriteFile(current, prompt.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(dir, "edited.md")
	if err := os.WriteFile(edited, []byte("SkillMaster 守则：\n我改过的一段。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("我自己的笔记，从来没有守则。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mdc := filepath.Join(dir, "rule.mdc")
	if err := os.WriteFile(mdc, prompt.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want int
		word string
	}{
		{current, 0, "当前"},
		{edited, 1, "被改过"},
		{notes, 1, "没有"},
		{filepath.Join(dir, "missing.md"), 1, "没有"},
		{mdc, 1, "读不到"},
	}
	for _, tc := range cases {
		stdout, _, status := runCaptured(t, "self", "rule", "check", tc.path)
		if status != tc.want {
			t.Errorf("%s status = %d, want %d", tc.path, status, tc.want)
		}
		if !strings.HasPrefix(stdout, tc.word) {
			t.Errorf("%s printed %q, want it to start with %q", tc.path, stdout, tc.word)
		}
	}
	if _, _, status := runCaptured(t, "self", "rule", "check"); status != 2 {
		t.Errorf("no path status = %d, want 2", status)
	}
	if _, _, status := runCaptured(t, "self", "rule", "check", current, current); status != 2 {
		t.Errorf("two paths status = %d, want 2", status)
	}
}

// `decline` is one boolean, and after it the notice stops (ADR 0249 §决定 4).
func TestSelfRuleDeclineRecordsTheAnswer(t *testing.T) {
	dir := isolateConfig(t)
	if _, _, status := runCaptured(t, "self", "rule", "decline"); status != 0 {
		t.Fatalf("decline status = %d", status)
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Declined {
		t.Error("decline did not record the answer")
	}
}
