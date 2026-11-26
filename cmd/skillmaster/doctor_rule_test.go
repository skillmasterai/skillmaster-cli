package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

type ruleLine struct{ state, detail string }

func ruleLines(dir string) []ruleLine {
	var out []ruleLine
	reportRuleState(dir, func(_, state, detail string) {
		out = append(out, ruleLine{state: state, detail: detail})
	})
	return out
}

func TestDoctorRuleStateIsNotOkWhenTheRuleIsGone(t *testing.T) {
	dir := isolateConfig(t)
	target := filepath.Join(os.Getenv("HOME"), "CLAUDE.md")
	writeFile(t, target, "原有\n")
	if _, err := rule.Install(dir, target, "claude-code", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("被删掉了\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := ruleLines(dir)
	if len(lines) != 1 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].state == "ok" {
		t.Errorf("doctor still says ok: %q", lines[0].detail)
	}
	if !strings.Contains(lines[0].detail, "被改过") {
		t.Errorf("detail = %q, want 被改过", lines[0].detail)
	}
}

func TestDoctorRuleStateSaysDeclined(t *testing.T) {
	dir := isolateConfig(t)
	if err := rule.SaveState(dir, rule.State{Schema: 1, Declined: true}); err != nil {
		t.Fatal(err)
	}
	lines := ruleLines(dir)
	if len(lines) != 1 || !strings.Contains(lines[0].detail, "已拒绝") {
		t.Fatalf("lines = %+v, want 已拒绝", lines)
	}
	// **Not counted as a failure.** "信息" is the state `doctor` leaves out of "有 N 项没通过";
	// a refusal the CLI obeyed must not read as a broken CLI.
	if lines[0].state != "信息" {
		t.Errorf("state = %q, want 信息", lines[0].state)
	}
}
