package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/rule"
)

// `install` — write the SkillMaster 守则 into one user-level instruction file.
//
// **The target is always `--file`** (ADR 0242): the person or the agent names the file; the CLI does
// not guess a default path. `--agent` only labels the record for `install --check` and the
// account-level report. `--yes` is the confirmation: writing into somebody's own file is never done
// silently.
type installOptions struct {
	file     string
	agent    string
	agentSet bool
	dryRun   bool
	yes      bool
	undo     bool
	check    bool
	decline  bool
}

func installCmd(args []string) error {
	opts, err := parseInstallArgs(args)
	if err != nil {
		return err
	}
	dir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("找不到用户配置目录：%w", err)
	}
	switch {
	case opts.check:
		return installCheck(dir)
	case opts.decline:
		return installDecline(dir)
	case opts.undo:
		res, err := rule.Undo(dir, opts.file)
		if err != nil {
			return err
		}
		fmt.Println(res.Plan)
		return nil
	case opts.dryRun:
		res, err := rule.InstallWithAgent(dir, opts.file, opts.agentPointer(), true)
		if err != nil {
			return err
		}
		fmt.Println(res.Plan)
		printNotice(res.Notice)
		fmt.Println("（预览：没有写任何文件。去掉 --dry-run 并加 --yes 才会写入。）")
		return nil
	case !opts.yes:
		res, err := rule.InstallWithAgent(dir, opts.file, opts.agentPointer(), true)
		if err != nil {
			return err
		}
		fmt.Println(res.Plan)
		printNotice(res.Notice)
		fmt.Fprintln(os.Stderr, "要写入请加 --yes。先确认这个路径是你的 Agent 的全局指令文件。")
		return &exitError{code: 2}
	}
	res, err := rule.InstallWithAgent(dir, opts.file, opts.agentPointer(), false)
	if err != nil {
		return err
	}
	fmt.Println(res.Plan)
	printNotice(res.Notice)
	fmt.Println("检查：skmr install --check")
	return nil
}

// printNotice shows a warning the write itself cannot fix — today, a Cursor `.mdc` without
// frontmatter, which Cursor ignores.
func printNotice(notice string) {
	if notice != "" {
		fmt.Fprintln(os.Stderr, "注意："+notice)
	}
}

// agentPointer keeps "flag absent" distinct from "flag given as an empty string".
func (o installOptions) agentPointer() *string {
	if !o.agentSet {
		return nil
	}
	return &o.agent
}

func parseInstallArgs(args []string) (installOptions, error) {
	var opts installOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			value, err := flagValue(args, i, "--file", "install")
			if err != nil {
				return opts, err
			}
			opts.file = value
			i++
		case "--agent":
			value, err := flagValue(args, i, "--agent", "install")
			if err != nil {
				return opts, err
			}
			opts.agent, opts.agentSet = value, true
			i++
		case "--dry-run":
			opts.dryRun = true
		case "--yes":
			opts.yes = true
		case "--undo":
			opts.undo = true
		case "--check":
			opts.check = true
		case "--decline":
			opts.decline = true
		default:
			return opts, usagef("%s", usageAt("install"))
		}
	}
	modes := 0
	for _, on := range []bool{opts.undo, opts.check, opts.decline} {
		if on {
			modes++
		}
	}
	if modes > 1 || (opts.dryRun && modes > 0) || (opts.yes && modes > 0) {
		return opts, usagef("%s", usageAt("install"))
	}
	if !opts.check && !opts.decline && opts.file == "" {
		return opts, usagef("要接 --file <路径>。%s", usageAt("install"))
	}
	if (opts.check || opts.decline) && (opts.file != "" || opts.agent != "") {
		return opts, usagef("--check 与 --decline 不接受 --file / --agent。%s", usageAt("install"))
	}
	if opts.agent != "" && opts.undo {
		return opts, usagef("--agent 只在写入时用。%s", usageAt("install"))
	}
	return opts, nil
}

func installCheck(dir string) error {
	// **The check reports, but under the same daily limit as every other command.** A person
	// troubleshooting must be able to run it twice without sending twice (and without two 3-second
	// waits on a dead network); the platform's answer is a row per day either way.
	defer reportRule(dir, false)
	st, _, stateErr := rule.LoadState(dir)
	// **A refusal wins over the records.** Someone who said "do not ask again" must not be told the
	// rule is missing the next time an agent deletes their instruction file: this command is what
	// startup reads, and it decides whether the journey asks a second time.
	if stateErr == nil && st.Declined {
		fmt.Println("这台电脑已拒绝安装守则（skmr install --decline）。要重新安装：skmr install --file <路径> --yes。")
		return nil
	}
	statuses, err := rule.Check(dir)
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		fmt.Println("这台电脑还没有安装记录。用 skmr install --file <路径> --yes 写一份。")
		return &exitError{code: 1}
	}
	bad, ignored := 0, 0
	for _, st := range statuses {
		switch st.State {
		case "present":
			// **A clean line would hide the leftover.** The state is present, but an exact old copy
			// beside it means the agent may read two rule sets; the detail is the only place to say so.
			if st.Detail != "" {
				fmt.Printf("已安装  %s（%s）\n", st.File.Path, st.Detail)
			} else {
				fmt.Printf("已安装  %s\n", st.File.Path)
			}
		case "legacy":
			// A command that may write replaces this exact block on its own; asking the person to
			// reinstall a file that will fix itself is the wrong instruction.
			fmt.Printf("旧版    %s（%s）\n", st.File.Path, st.Detail)
		case "unmanaged":
			fmt.Printf("不管    %s（项目级、符号链接或不是普通文件；skmr install --file %s --undo 可去掉这条记录）\n", st.File.Path, st.File.Path)
		case "ignored":
			// **Rewriting does not fix this one.** `install --file` would write the same block into
			// the same frontmatter-less file, so the remedy is the notice's, not the rewrite line.
			ignored++
			fmt.Printf("读不到  %s（%s）\n", st.File.Path, st.Detail)
		case "absent":
			bad++
			fmt.Printf("不见了  %s（%s）\n", st.File.Path, st.Detail)
		default:
			bad++
			fmt.Printf("被改过  %s（%s）\n", st.File.Path, st.Detail)
		}
	}
	if ignored > 0 {
		fmt.Println("补 frontmatter：先 skmr prompt print 取守则正文，连同 frontmatter 写回同一个文件，再 skmr install --file <路径> --yes 登记。")
	}
	if bad > 0 {
		fmt.Println("重写：skmr install --file <上面的路径> --yes")
	}
	if bad+ignored > 0 {
		return &exitError{code: 1}
	}
	return nil
}

func installDecline(dir string) error {
	st, _, err := rule.LoadState(dir)
	if err != nil {
		return err
	}
	st.Declined = true
	if err := rule.SaveState(dir, st); err != nil {
		return err
	}
	fmt.Println("已记下：不再提示安装守则；之前的安装记录不再自动迁移。")
	return nil
}

// installedRuleLine is startup's local answer to "which agents already carry the rule?".
func installedRuleLine() (string, bool) {
	dir, err := config.Dir()
	if err != nil {
		return "", false
	}
	st, ok, err := rule.LoadState(dir)
	if err != nil || !ok || st.Declined || len(st.Files) == 0 {
		return "", false
	}
	// **Only files that still carry the rule.** A record outlives its file, and saying "already
	// written" about a path that lost the rule (or one this command never manages) contradicts the
	// `install --check` line right below it.
	statuses, err := rule.Check(dir)
	if err != nil {
		return "", false
	}
	labels := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.State != "present" {
			continue
		}
		// **The same label the report and the record use.** An old `prompt.json` value can be
		// `Claude Code` or Chinese; showing it verbatim makes one agent look like two.
		label := rule.NormalizeAgent(status.File.Agent)
		if label == "unknown" {
			label = "未命名"
		}
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return "", false
	}
	return "已经写入守则的 Agent：" + strings.Join(labels, "、") + "。检查：skmr install --check", true
}

// readOnlyRuleMode says whether this invocation promised not to write.
//
// **Every `install` without `--yes` is read-only, including the ones that are about to fail.** The
// command line itself says "不加只预览", so `--file` alone, a missing `--file`, or a misspelt flag
// must not migrate recorded files before printing the preview or the usage error.
func readOnlyRuleMode(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "doctor" {
		return true
	}
	if args[0] != "install" {
		return false
	}
	// A mode that only reads wins even when `--yes` is also present: the combination is a usage
	// error, and an error must not migrate files before it is printed.
	for _, flag := range []string{"--check", "--dry-run", "--undo", "--decline"} {
		if hasFlag(args[1:], flag) {
			return true
		}
	}
	// **`--yes` without a usable `--file` is a usage error too** (`install --yes`, `install --file
	// --yes`). Main parses before migrating, and this keeps the two answers from drifting apart.
	return !hasFlag(args[1:], "--yes") || !installTargets(args[1:])
}

// installTargets reports whether the args carry a `--file` with a value that is not another flag.
func installTargets(args []string) bool {
	for i, arg := range args {
		if arg == "--file" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			return true
		}
	}
	return false
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

// pointRuleReadsAtRealState keeps the records readable when the writable state directory is a
// sandbox fallback.
//
// `config.Dir()` answers "where can this process write", and inside an agent's sandbox that is a
// temp directory holding no records, while the machine's real install.json is still readable in the
// ordinary directory (measured with sandbox-exec, 2026-11-26: `install --check` said 「这台电脑还没
// 有安装记录」 on a machine with three). Read-only answers — `--check`, `doctor`, the coverage
// report — have to come from the real file; writes keep going to `dir`.
func pointRuleReadsAtRealState(dir string) {
	if _, err := os.Stat(rule.StatePath(dir)); err == nil {
		return
	}
	_, older, err := config.StatePaths("install.json")
	if err != nil || older == "" {
		return
	}
	rule.SetStateReadPath(older)
}
