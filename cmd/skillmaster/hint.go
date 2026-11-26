package main

import (
	"fmt"
	"os"

	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// hintText renders the one line ADR 0246 allows, or "" when there is nothing to say.
//
// **Each message names the fact and the first command, nothing else.** The recorded-file line says
// 「没有可用的守则」 rather than 「没找到」: the same record covers a file emptied, edited by hand,
// or written without the frontmatter its editor needs, and only the first is "not found". Its first
// step is `install --check` because `install --file` refuses an edited file — sending the agent
// straight there would hand it a command whose answer is no.
//
// **The nothing-installed line names no paths.** Which file an agent reads is the agent's own fact,
// and a list here would be the default-path table ADR 0242 keeps out of agent output.
func hintText(h rule.Hint) string {
	switch h.Kind {
	case rule.HintRecordedMissing:
		return fmt.Sprintf("（守则更新：%s 里没有可用的守则——缺了、被改过，或 Cursor 读不到。先运行 skmr install --check；确实是缺了，再让用户确认后运行 skmr install --file %s --agent %s --yes。）",
			h.Path, h.Path, h.Agent)
	case rule.HintUnreadableDefault:
		return fmt.Sprintf("（守则更新：%s 里有守则，但缺 frontmatter，Cursor 读不到。补好文件开头（frontmatter：description 与 alwaysApply: true），再 skmr install --file %s --agent %s --yes，先问用户。）",
			h.Path, h.Path, h.Agent)
	case rule.HintOutdatedDefault:
		return fmt.Sprintf("（守则更新：%s 里是旧版守则。登记并更新：skmr install --file %s --agent %s --yes，先问用户。）",
			h.Path, h.Path, h.Agent)
	case rule.HintNothingInstalled:
		return "（守则更新：这台机器还没有守则。让 Agent 找到自己的全局记忆文件——不知道就问用户——再用 skmr install --file <路径> --agent <名> --yes 登记。）"
	}
	return ""
}

// hintRuns is the seam TestMain closes, for the reason machineStore is replaced there: a test that
// runs a command must not write a state file into this machine's real config directory — the notice
// would then be spent on the person's own machine by `go test`. Tests about the notice call
// hintRule directly, and the two that exercise the wiring open it again for their own run.
var hintRuns = true

// hintAllowed keeps the notice on commands that use the rule, not on ones that talk about the
// binary or run offline by contract. `--version` never reaches here (it returns before the defer is
// registered), and `doctor` is already read-only via `readOnlyRuleMode`.
func hintAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	// A map rather than a switch: the command-tree test reads `switch args[0]` as a dispatcher and
	// demands a declaration for every name in it, which this list is not. `update` is here because
	// it is about the binary and runs before the new build is in place.
	skip := map[string]bool{"help": true, "prompt": true, "update": true}
	return !skip[args[0]]
}

// hintRule prints at most one line per rule version (ADR 0246).
//
// **Nothing is printed on stdout and nothing changes the command's result.** The state file is
// written after the line, so a machine that sees the notice never sees it twice for the same rule;
// a write failure only means the next command may repeat it. A ledger that cannot be read says
// nothing and spends nothing (ADR 0246: the notice is about a version, not a guess).
func hintRule(dir string) {
	if st, _, err := rule.LoadState(dir); err == nil && st.Declined {
		return
	}
	if rule.HintedVersion(dir) >= rule.Version {
		return
	}
	hint := rule.NeedHint(dir)
	if hint.Kind == rule.HintUnreadable {
		return
	}
	if text := hintText(hint); text != "" {
		fmt.Fprintln(os.Stderr, text)
	}
	_ = rule.MarkHinted(dir, rule.Version)
}
