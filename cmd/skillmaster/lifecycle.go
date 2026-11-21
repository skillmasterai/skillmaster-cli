package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
)

// The CLI's lifecycle verbs (v10): creating a skill from nothing, the collaborator list and its
// revocation, asking for access, and opening the page an action lives on.
//
// **What is not here is as deliberate as what is.** Publishing, rolling back, revoking a version,
// restoring a version, taking a skill offline and deleting one change what a caller reads; they live
// on the browser plane (ADR 0061), and the refusal in version.go hands the person the exact page.

// jsonModeIn answers whether `--json` is on the line before the command has parsed anything, so a
// usage error that comes first still answers in the shape the flag promises. Each command names its
// own value-taking flags: `--status` takes a value for `version list` and is a switch here, and a
// shared table cannot know both.
func jsonModeIn(args []string, valueFlags ...string) bool {
	takesValue := make(map[string]bool, len(valueFlags))
	for _, flag := range valueFlags {
		takesValue[flag] = true
	}
	for i := 0; i < len(args); i++ {
		if takesValue[args[i]] {
			i++
			continue
		}
		if args[i] == "--json" {
			return true
		}
	}
	return false
}

func createSkill(ctx context.Context, args []string) error {
	name := ""
	description := ""
	versionName := ""
	nameGiven := false
	jsonMode := jsonModeIn(args, "--description", "--name")
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--description":
			if i+1 >= len(args) {
				return versionErr(jsonMode, usagef("--description 后面要跟描述。%s", usageAt("skill create")), "")
			}
			description = args[i+1]
			i++
		case "--name":
			if i+1 >= len(args) {
				return versionErr(jsonMode, usagef("--name 后面要跟版本名称。%s", usageAt("skill create")), "")
			}
			versionName = args[i+1]
			nameGiven = true
			i++
		case "--json":
			jsonMode = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return versionErr(jsonMode, unknownFlag("skill create", args[i]), "")
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 {
		return versionErr(jsonMode, usagef("%s", usageAt("skill create")), "")
	}
	name = positional[0]
	if name == "" {
		return versionErr(jsonMode, usagef("%s", usageAt("skill create")), "")
	}
	if description == "" {
		return versionErr(jsonMode, usagef("新建 skill 要有一句描述（--description）。%s", usageAt("skill create")), "")
	}
	if strings.ContainsAny(name, "/@") {
		return versionErr(jsonMode, usagef("名字是一段，不能带 / 或 @（%s）。", name), "")
	}
	// Omitted is "第一版"; **given empty is a request for a nameless version**, which the server
	// refuses (`draft_name_required`) and the CLI must not paper over by filling in a default.
	if nameGiven && strings.TrimSpace(versionName) == "" {
		return versionErr(jsonMode, usagef("版本名称不能是空的（--name）。%s", usageAt("skill create")), "")
	}
	if !nameGiven {
		versionName = "第一版"
	}

	created, err := authenticated(ctx, func(client api.Client) (api.CreatedSkill, error) {
		return client.CreateSkill(ctx, name, description, versionName)
	})
	if err != nil {
		return versionErr(jsonMode, err, "")
	}
	if jsonMode {
		return writeJSON(created)
	}
	fmt.Printf("已新建 %s/%s@%s（版本名称：%s）。\n", created.Namespace, created.Name,
		created.VersionCode, created.VersionName)
	return nil
}

func requestAccess(ctx context.Context, args []string) error {
	address := ""
	rung := ""
	wantStatus := false
	wantCancel := false
	jsonMode := jsonModeIn(args, "--role")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				return versionErr(jsonMode, usagef("--role 后面要跟档位。%s", usageAt("skill request")), "")
			}
			rung = args[i+1]
			i++
		case "--status":
			wantStatus = true
		case "--cancel":
			wantCancel = true
		case "--json":
			jsonMode = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return versionErr(jsonMode, unknownFlag("skill request", args[i]), "")
			}
			if address != "" {
				return versionErr(jsonMode, usagef("%s", usageAt("skill request")), "")
			}
			address = args[i]
		}
	}
	if rung != "" && !slices.Contains(api.Roles, rung) {
		return versionErr(jsonMode, usagef("档位只能是 %s，收到 %q。%s",
			strings.Join(api.Roles, "、"), rung, usageAt("skill request")), "")
	}
	chosen := 0
	if rung != "" {
		chosen++
	}
	if wantStatus {
		chosen++
	}
	if wantCancel {
		chosen++
	}
	if address == "" || chosen != 1 {
		return versionErr(jsonMode, usagef("要给一个 skill 地址，并且 --role、--status、--cancel 里只给一个。%s", usageAt("skill request")), "")
	}
	if err := checkAddressSyntax("skill request", address); err != nil {
		return versionErr(jsonMode, err, "")
	}

	switch {
	case wantStatus:
		pending, err := authenticated(ctx, func(client api.Client) (*string, error) {
			return client.PendingAccess(ctx, address)
		})
		if err != nil {
			return versionErr(jsonMode, err, "")
		}
		if jsonMode {
			return writeJSON(map[string]any{"pending_rung": pending})
		}
		if pending == nil {
			fmt.Println("没有在等的申请。")
		} else {
			fmt.Printf("正在等待「%s」的审批。\n", rungLabel(*pending))
		}
		return nil

	case wantCancel:
		if _, err := authenticated(ctx, func(client api.Client) (struct{}, error) {
			return struct{}{}, client.WithdrawAccess(ctx, address)
		}); err != nil {
			return versionErr(jsonMode, err, "")
		}
		if jsonMode {
			return writeJSON(map[string]any{"cancelled": true})
		}
		fmt.Println("已撤回申请。")
		return nil

	default:
		asked, err := authenticated(ctx, func(client api.Client) (string, error) {
			return client.RequestAccess(ctx, address, rung)
		})
		if err != nil {
			return versionErr(jsonMode, err, "")
		}
		if jsonMode {
			return writeJSON(map[string]any{"rung": asked})
		}
		fmt.Printf("已申请「%s」档，等待审批。\n", rungLabel(asked))
		return nil
	}
}

// tabRung is the console's own gate for each section (`useSkillPageRoute.ts`): 信息 and 版本 exist
// for every caller, 文件 starts at browse, 设置 at admin.
func tabRung(tab string) string {
	switch tab {
	case "files":
		return "browse"
	case "settings":
		return "admin"
	default:
		return "invoke"
	}
}

var tabNames = map[string]string{
	"": "信息", "info": "信息", "files": "文件", "versions": "版本", "settings": "设置",
}

func openSkill(ctx context.Context, args []string) error {
	address := ""
	tab := ""
	sub := ""
	printOnly := false
	jsonMode := jsonModeIn(args, "--tab", "--sub")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tab":
			if i+1 >= len(args) {
				return versionErr(jsonMode, usagef("--tab 后面要跟分区。%s", usageAt("skill open")), "")
			}
			tab = args[i+1]
			i++
		case "--sub":
			if i+1 >= len(args) {
				return versionErr(jsonMode, usagef("--sub 后面要跟子页。%s", usageAt("skill open")), "")
			}
			sub = args[i+1]
			i++
		case "--print":
			printOnly = true
		case "--json":
			jsonMode = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return versionErr(jsonMode, unknownFlag("skill open", args[i]), "")
			}
			if address != "" {
				return versionErr(jsonMode, usagef("%s", usageAt("skill open")), "")
			}
			address = args[i]
		}
	}
	if address == "" {
		return versionErr(jsonMode, usagef("%s", usageAt("skill open")), "")
	}
	if strings.Contains(address, "@") {
		return versionErr(jsonMode, usagef("这个命令打开的是整个 skill；要看某一版自己的页面，用 version open。%s", usageAt("skill open")), "")
	}
	if tab != "" && tab != "info" && tab != "files" && tab != "versions" && tab != "settings" {
		return versionErr(jsonMode, usagef("--tab 只接受 info、files、versions、settings。%s", usageAt("skill open")), "")
	}
	if sub != "" && tab != "settings" {
		return versionErr(jsonMode, usagef("--sub 只在 --tab settings 下有效。%s", usageAt("skill open")), "")
	}
	if sub != "" && sub != "general" && sub != "collaborators" && sub != "details" {
		return versionErr(jsonMode, usagef("--sub 只接受 general、collaborators、details。%s", usageAt("skill open")), "")
	}

	namespace, name, ok := strings.Cut(address, "/")
	if !ok || namespace == "" || name == "" || strings.Contains(name, "/") {
		return versionErr(jsonMode, usagef("地址要写成 <命名空间>/<名字>。%s", usageAt("skill open")), "")
	}
	target := skillWebURL(address, tab, sub)
	// **Each section has its own rung** (ADR 0051): 信息 and 版本 are open to a caller who may only
	// invoke; 文件 starts at browse; 设置 at admin. The fact is read once, and a section the caller
	// cannot reach is refused rather than opened empty.
	facts := skillFacts(ctx, address)
	if facts.known {
		if required := tabRung(tab); !rungAllows(facts.rung, facts.owned, required) {
			return pageRefusal(jsonMode, "insufficient_permission",
				fmt.Sprintf("%s分区要 %s 档，你在这个 skill 上是 %s。",
					tabNames[tab], rungNames[required], rungNames[facts.rung]), target)
		}
	}
	if !facts.known {
		fmt.Fprintln(os.Stderr,
			"（没能预检：读不到这个 skill 的档位（没有可用凭据、网络失败，或这把凭据的范围不够）；页面会按你的登录状态显示。）")
	}

	opened := false
	warning := ""
	if !printOnly {
		if openErr := openBrowser(target); openErr != nil {
			warning = fmt.Sprintf("没能自动打开浏览器（地址在上面，也可以自己打开）：%v", openErr)
		} else {
			opened = true
		}
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	if jsonMode {
		return writeJSON(struct {
			URL    string `json:"url"`
			Opened bool   `json:"opened"`
		}{URL: target, Opened: opened})
	}
	fmt.Println(target)
	switch {
	case opened:
		fmt.Println("（已在浏览器里打开。）")
	case printOnly:
		fmt.Println("（--print：只打印地址，没有打开浏览器。）")
	default:
		fmt.Println("（没能自动打开浏览器；地址就在上面。）")
	}
	return nil
}

// ---------------------------------------------------------------------------
// v14: the library reference, and restoring a deleted skill
// ---------------------------------------------------------------------------

// joinLibrary adds or removes a library reference. **`--yes` is required and the consequence is
// printed first** (PRD §CLI 命令面 §`skill`): joining is additive but changes what a later listing
// shows, and leaving takes a group of private tags out of view until the skill is joined again. A
// caller with nobody in front of it gets a non-zero exit and the exact command to run, not a prompt.
func joinLibrary(ctx context.Context, args []string, join bool) error {
	address := ""
	yes := false
	joinOnly := false
	for _, arg := range args {
		switch arg {
		case "--yes":
			yes = true
		case "--join-only":
			if join {
				return unknownFlag(joinLeavePath(join), arg)
			}
			joinOnly = true
		default:
			if strings.HasPrefix(arg, "-") {
				return unknownFlag(joinLeavePath(join), arg)
			}
			if address != "" {
				return usagef("%s", usageAt(joinLeavePath(join)))
			}
			address = arg
		}
	}
	if address == "" {
		return usagef("%s", usageAt(joinLeavePath(join)))
	}
	if err := checkBareAddress(address); err != nil {
		return err
	}

	verb, action := "join", "加入"
	if !join {
		verb, action = "leave", "移出库"
	}
	command := fmt.Sprintf("skmr skill %s %s --yes", verb, address)
	if joinOnly {
		command += " --join-only"
	}
	if !yes {
		// **No write is sent.** The consequence is on stdout (it is the answer to "what would this
		// do"), and the refusal is what makes the exit non-zero. Leave reads the caller's relation
		// first — a read is not an execution — so the sentence names the real consequence (v89,
		// ADR 0231); joining has only one consequence and sends nothing.
		if join {
			fmt.Println("加入是引用：不复制内容、不产生版本、不改变访问权；作者发新版，库里跟新版。")
		} else if joinOnly {
			// **The narrow undo never ends a share**, so it must not borrow the exit's sentence:
			// a shared entry's whole-exit consequence would be a promise this flag does not keep.
			fmt.Println(leaveJoinOnly)
		} else {
			fmt.Println(leaveConsequence(ctx, address))
		}
		fmt.Printf("确认后执行：\n%s\n", command)
		return fmt.Errorf("没有执行：%s要加 --yes。", action)
	}

	change, err := authenticated(ctx, func(client api.Client) (api.Change, error) {
		if join {
			return client.JoinLibrary(ctx, address)
		}
		return client.LeaveLibrary(ctx, address, joinOnly)
	})
	if err != nil {
		// **The migration command names the skill.** The shared server sentence cannot — it does
		// not know which address the caller typed — so the command prints its own, runnable one.
		var server *api.ServerError
		if errors.As(err, &server) && server.Code == "skill_offline_pending" {
			until := server.Details["pending_until"]
			if len(until) >= 10 {
				until = until[:10]
			}
			copyCommand := "skmr skill copy " + address + " --name <新名字>"
			if until != "" {
				return fmt.Errorf("这个 skill 正在下线中，将于 %s 正式下线，不能再入库。"+
					"要保留它，复制到自己的库：%s。", until, copyCommand)
			}
			return fmt.Errorf("这个 skill 正在下线中，不能再入库。"+
				"要保留它，复制到自己的库：%s。", copyCommand)
		}
		// **The narrow undo has one refusal the generic sentence gets wrong.** A shared entry is
		// in the library — it just has no join reference to take back; saying "not in your library"
		// would send a reader (or an agent) looking for the wrong fix. The same code also covers an
		// address the caller never joined, so the sentence does not presume a share.
		if joinOnly && server != nil && server.Code == "library_reference_missing" {
			return fmt.Errorf("这条没有可撤销的加入引用。如果它是分享给你的，移出库会结束共享："+
				"skmr skill leave %s --yes；否则用 skmr skills list 核对。", address)
		}
		return v14Error(err)
	}
	if join {
		if !change.Changed {
			// The reference was already there, including the sleeping kind — and a sleeping
			// reference is still a reference (PRD #3), so "nothing changed" is the honest answer.
			fmt.Println("已经在你的库里。")
			return nil
		}
		fmt.Printf("已把 %s 收进你的库。\n", address)
		return nil
	}
	if joinOnly {
		// The share, if any, is still there — the entry did not necessarily leave the library.
		fmt.Printf("已撤销你在 %s 上的加入引用。\n", address)
		return nil
	}
	fmt.Printf("已把 %s 移出你的库。\n", address)
	return nil
}

// The sentences a leave can print before asking for --yes. **The source is read, not
// guessed**: a shared entry and a joined one end differently (ADR 0231), and this sentence is what
// a person reads before an irreversible action. The fallback covers both when the read cannot
// answer (no credential, a dormant entry).
const (
	/* **Only durable facts.** Whether the skill can be called or rejoined right now is not this
	   sentence's to promise: an offline skill, or one inside its 30-day ramp, refuses joins and
	   answers 503. "Still in the public library" is a property the action does not change. */
	leaveJoined = "移出的是「我加入的」这条引用。我的标签与我的适用场景会保留；它仍在公开库。"
	// The discovery page's undo: only the join it made goes; a share that arrived since stays.
	leaveJoinOnly = "撤销只删「我加入的」这条引用，不结束任何共享；如果期间收到过共享，" +
		"条目和那段共享都会保留。我的标签与我的适用场景会保留。"
	leaveShared = "移出会结束这段共享，你会失去对它的访问权；要回来，需要分享方重新邀请你。" +
		"我的标签与我的适用场景会保留。"
	// A public skill's share only takes away the rung it gave: the public rung stays.
	leaveSharedPublic = "移出会结束这段共享，你会失去对方给你的权限；它仍是公开的。" +
		"我的标签与我的适用场景会保留。"
	leaveCreated      = "这个 skill 是你创建的，不能移出库；要让它不再出现，请在控制台里删除它。"
	leaveNotInLibrary = "这个 skill 不在你的库里，没有可移出的内容。"
	// An offline skill (or one that stopped being public) still resolves, but there is nothing to
	// consume right now; "still callable" would be false, so the sentence names the dormancy.
	leaveDormant = "这条库条目现在不生效（skill 已下线或暂时不可见）。移出会删掉你这一条；" +
		"如果它是别人共享给你的，这段共享也一起结束。我的标签与我的适用场景会保留。"
	leaveUnknown = "移出会删掉「我加入的」这条引用；如果它是别人共享给你的，这段共享会结束——" +
		"私有的你会失去访问权，公开的公开档仍在。我的标签与我的适用场景会保留。"
)

// leaveConsequence reads the caller's relation so the confirmation names the truth. It never
// starts a login (the `--no-login` mode): a question that only prints a sentence must not open a
// browser. Any failure falls back to the sentence that covers both sources.
func leaveConsequence(ctx context.Context, address string) string {
	library, err := authenticatedWith(ctx, true,
		func(client api.Client) (api.LibraryView, error) {
			tags, err := client.Tags(ctx, address)
			if err != nil {
				return api.LibraryView{}, err
			}
			return tags.Library, nil
		})
	if err != nil {
		return leaveUnknown
	}
	switch library.Source {
	case "joined":
		if !library.Active {
			return leaveDormant
		}
		return leaveJoined
	case "shared":
		// Dormant first: the rung is still a fact, but the skill cannot be called right now.
		if !library.Active {
			return leaveDormant
		}
		// A public shared skill keeps the public rung, so it is not an access loss.
		if library.PublicRung != nil {
			return leaveSharedPublic
		}
		return leaveShared
	case "created":
		return leaveCreated
	case "":
		return leaveNotInLibrary
	default:
		return leaveUnknown
	}
}

// restoreSkill brings a soft-deleted skill back. **No `--yes`**: the action is reversible in the
// direction that matters — the skill can be deleted again from the console — and it changes
// visibility back to what it was rather than opening anything new.
func restoreSkill(ctx context.Context, args []string) error {
	address := ""
	for _, arg := range args {
		switch arg {
		default:
			if strings.HasPrefix(arg, "-") {
				return unknownFlag("skill restore", arg)
			}
			if address != "" {
				return usagef("%s", usageAt("skill restore"))
			}
			address = arg
		}
	}
	if address == "" {
		return usagef("%s", usageAt("skill restore"))
	}
	if err := checkBareAddress(address); err != nil {
		return err
	}

	restored, err := authenticated(ctx, func(client api.Client) (api.Restored, error) {
		return client.RestoreSkill(ctx, address)
	})
	if err != nil {
		return v14Error(err)
	}
	// v61 (ADR 0183): the name may have been taken while the skill was deleted; the restore then
	// answers under 原名-restored-<id>, and saying the old address would be false.
	target := restored.Address
	if target == "" {
		target = address
	}
	if restored.Renamed {
		fmt.Printf("已恢复。原名已被占用，这个 skill 现在叫 %s。\n", target)
		return nil
	}
	fmt.Printf("已恢复 %s。\n", target)
	return nil
}

// rungLabel is the interface's word for a wire rung (voice-spec §名词表: 调用 / 浏览 / 写入 /
// 维护 / 管理). The wire value is still what `--json` carries; this is only the sentence.
func rungLabel(rung string) string {
	switch rung {
	case "invoke":
		return "调用"
	case "browse":
		return "浏览"
	case "write":
		return "写入"
	case "maintain":
		return "维护"
	case "admin":
		return "管理"
	default:
		return rung
	}
}
