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

// **No client file, no green line.** A machine whose agent keeps its memory somewhere this build
// does not know is not a machine with the rule; the line says what to do instead of guessing
// (ADR 0249).
func TestDoctorRuleStateIsNotOkWithoutAKnownClientFile(t *testing.T) {
	dir := isolateConfig(t)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	lines := ruleLines(dir)
	if len(lines) != 1 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].state == "ok" {
		t.Errorf("doctor says ok with no client file: %q", lines[0].detail)
	}
	if !strings.Contains(lines[0].detail, "没有检测到") {
		t.Errorf("detail = %q, want 没有检测到", lines[0].detail)
	}
}

// **The line describes the disk, not a record.** A client file without the current rule is the
// state the update notice is about, and `doctor` must not call it ok.
func TestDoctorRuleStateIsNotOkWhenTheClientFileHasNoRule(t *testing.T) {
	dir := isolateConfig(t)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	target := filepath.Join(os.Getenv("HOME"), ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("原有内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := ruleLines(dir)
	if len(lines) != 1 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].state == "ok" {
		t.Errorf("doctor says ok without the rule: %q", lines[0].detail)
	}
	if !strings.Contains(lines[0].detail, "0 个有当前守则") {
		t.Errorf("detail = %q, want the count", lines[0].detail)
	}
}

func TestDoctorRuleStateSaysOkWhenEveryKnownClientHasIt(t *testing.T) {
	dir := isolateConfig(t)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	target := filepath.Join(os.Getenv("HOME"), ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := ruleLines(dir)
	if len(lines) != 1 || lines[0].state != "ok" {
		t.Fatalf("lines = %+v, want one ok line", lines)
	}
	if !strings.Contains(lines[0].detail, "1 个客户端：1 个有当前守则") {
		t.Errorf("detail = %q", lines[0].detail)
	}
}

func TestDoctorRuleStateSaysDeclined(t *testing.T) {
	dir := isolateConfig(t)
	if err := rule.SetDeclined(dir, true); err != nil {
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

// A Cursor rule file without frontmatter is read by nothing, so detection must not count it
// (v94's `FileHasRule` semantics, kept).
func TestDoctorRuleStateDoesNotCountAnIgnoredCursorFile(t *testing.T) {
	dir := isolateConfig(t)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	target := filepath.Join(os.Getenv("HOME"), ".cursor", "rules", "skillmaster.mdc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, rule.Block(), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := ruleLines(dir)
	if len(lines) != 1 || lines[0].state == "ok" {
		t.Fatalf("lines = %+v, want a non-ok line", lines)
	}
	if !strings.Contains(lines[0].detail, "0 个有当前守则") {
		t.Errorf("detail = %q", lines[0].detail)
	}
}
