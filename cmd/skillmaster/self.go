package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// `self` — what this CLI manages about itself: its binary, its diagnosis, and the rule it carries.
//
// **Grouping is the decision, not the names** (ADR 0248): `update`, `doctor` and the rule used to be
// three separate top-level doors, and nothing said they were one thing.
func selfCmd(ctx context.Context, args []string) (err error) {
	if len(args) == 0 {
		return usagef("%s", usageAt("self"))
	}
	defer func() { err = retargetUsage(err, "self "+args[0]) }()
	switch args[0] {
	case "update":
		return updateCmd(ctx, args[1:])
	case "doctor":
		return doctorCmd(ctx, args[1:])
	case "rule":
		return selfRule(args[1:])
	default:
		return unknownName("self", args[0], childNames("self"))
	}
}

// selfRule is the rule's three verbs. **It only reads** (ADR 0249): the file belongs to the agent,
// and the CLI writes neither it nor a record of it.
func selfRule(args []string) (err error) {
	if len(args) == 0 {
		return usagef("%s", usageAt("self rule"))
	}
	defer func() { err = retargetUsage(err, "self rule "+args[0]) }()
	switch args[0] {
	case "print":
		if len(args) > 1 {
			return unknownFlag("self rule print", args[1])
		}
		os.Stdout.Write(rule.Block())
		return nil
	case "check":
		if len(args) != 2 {
			return usagef("%s", usageAt("self rule check"))
		}
		// **A flag is not a file name.** `self rule check --not-a-flag` would otherwise answer
		// 「没有」 about a path that never existed, which reads as a fact instead of a typo.
		if strings.HasPrefix(args[1], "-") {
			return unknownFlag("self rule check", args[1])
		}
		return ruleCheck(args[1])
	case "decline":
		if len(args) > 1 {
			return unknownFlag("self rule decline", args[1])
		}
		return ruleDecline()
	default:
		return unknownName("self rule", args[0], childNames("self rule"))
	}
}

// ruleCheckWords are the words v94's `install --check` used, kept because agents and docs already
// speak them (ADR 0249).
var ruleCheckWords = map[string]string{
	"present":   "当前",
	"legacy":    "旧版",
	"modified":  "被改过",
	"ignored":   "读不到",
	"absent":    "没有",
	"unmanaged": "不管",
}

// ruleCheck answers one file's state, and only answers it.
//
// **Exit 0 means "the editor reads the current rule"; every other answer is 1**, which is what lets
// an agent run it as a check. A usage error stays 2 through the caller.
func ruleCheck(path string) error {
	state, detail := rule.CheckPath(path)
	word := ruleCheckWords[state]
	if word == "" {
		word = state
	}
	shown := path
	if abs, err := rule.ExpandPath(path); err == nil {
		shown = abs
	}
	if detail != "" {
		fmt.Printf("%s  %s（%s）\n", word, shown, detail)
	} else {
		fmt.Printf("%s  %s\n", word, shown)
	}
	// **What changed, where the file is wrong.** The ordinary notice is off for the whole
	// `self rule` group (see hintAllow); this is the one place it still helps, because the agent has
	// just found an old or edited block and needs the reason to update it. stderr, because stdout
	// answers the question and nothing is written from this output.
	if state == "legacy" || state == "modified" {
		if note := rule.VersionNote(rule.Version); note != "" {
			fmt.Fprintf(os.Stderr, "守则已到第 %d 版：%s\n", rule.Version, sentence(note))
		}
	}
	if state == "present" {
		return nil
	}
	return &exitError{code: 1}
}

// ruleDecline records "stop asking" and nothing else.
func ruleDecline() error {
	dir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("找不到用户配置目录：%w", err)
	}
	if err := rule.SetDeclined(dir, true); err != nil {
		return err
	}
	fmt.Println("已记下：不再自动提示守则更新。")
	return nil
}

// installedRuleLine is startup's local answer to "which agents already carry the rule?".
//
// **It reads the files, not a record.** A record outlives its file; the line is printed right above
// the instructions for writing the rule, so it has to describe the disk.
func installedRuleLine() (string, bool) {
	dir, err := config.Dir()
	if err != nil {
		return "", false
	}
	st, _, err := rule.LoadLocalState(dir)
	if err != nil || st.Declined {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	labels := make([]string, 0, 4)
	for _, detection := range rule.Detect(home) {
		if detection.Present {
			labels = append(labels, detection.Agent)
		}
	}
	if len(labels) == 0 {
		return "", false
	}
	return "已经写入守则的 Agent：" + strings.Join(labels, "、") + "。检查：skmr self rule check <路径>", true
}
