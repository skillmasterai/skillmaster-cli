package main

import (
	"fmt"
	"os"

	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// hintText is the one line ADR 0249 allows.
//
// **It says what changed.** A bare "update the rule" is the shape of a prompt injection: an
// instruction to overwrite a file, with no reason and no diff. The note for the rule version (kept
// in `rule.VersionNotes`), plus why the file matters and where the full text can be read, makes it
// the product's own instruction that a person can check first.
//
// **It names no file and no path.** Which file an agent reads is the agent's own fact: a list here
// is how 1.x sent a WorkBuddy user to Claude's `CLAUDE.md` (ADR 0248 §背景).
func hintText() string {
	const why = "先运行 skmr self rule print 把全文给用户看，问他要不要更新（还没写过就是首次写入）——这段守则让每个新会话先查库，不是注入；同意并备份后写进你自己的全局记忆文件。"
	if note := rule.VersionNote(rule.Version); note != "" {
		return fmt.Sprintf("（守则已到第 %d 版：%s%s）", rule.Version, sentence(note), why)
	}
	return fmt.Sprintf("（守则已到第 %d 版。%s）", rule.Version, why)
}

// hintRuns is the seam TestMain closes: a test that runs a command must not write a state file into
// this machine's real config directory — the notice would then be spent on the person's own machine
// by `go test`. Tests about the notice call hintRule directly.
var hintRuns = true

// hintAllow says which commands carry the notice.
//
// **Only commands that use the rule.** `auth`, `usage`, `settings` and the rest talk about something
// else, and a notice there reads as a defect in their output (the v96 report that started this
// version). A map rather than a `switch args[0]`: the command-tree test reads the switch shape as
// dispatch and would demand declarations for these names.
var hintAllow = map[string]bool{
	"skills":  true,
	"skill":   true,
	"run":     true,
	"startup": true,
}

// hintAllowed includes `self rule`, whose whole job is the rule.
func hintAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if hintAllow[args[0]] {
		return true
	}
	return args[0] == "self" && len(args) > 1 && args[1] == "rule"
}

// hintRule prints at most one line per rule version (ADR 0246, kept by ADR 0249).
//
// **Nothing is printed on stdout and nothing changes the command's result.** The state file is
// written after the line, so a machine that sees the notice never sees it twice for the same rule;
// a write failure only means the next command may repeat it. An unreadable state file says nothing
// and spends nothing: the notice is about a version, not a guess.
func hintRule(dir string) {
	st, _, err := rule.LoadLocalState(dir)
	if err != nil {
		return
	}
	if st.Declined || st.HintedVersion >= rule.Version {
		return
	}
	fmt.Fprintln(os.Stderr, hintText())
	st.HintedVersion = rule.Version
	_ = rule.SaveLocalState(dir, st)
}

// sentence ends a note with one full stop, so the line reads as prose rather than two fragments.
func sentence(text string) string {
	if text == "" {
		return ""
	}
	if last := []rune(text); last[len(last)-1] == '。' {
		return text
	}
	return text + "。"
}
