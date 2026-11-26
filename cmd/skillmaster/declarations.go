package main

import (
	"sort"

	"github.com/skillmasterai/skillmaster-cli/internal/help"
)

// commandHelp is the whole command table: every executable path, the pure groups, the top level,
// and the topics. The declarations live here, next to the handlers, because the help and the
// command have one home (v16 做 #7). One declaration per command; the command-tree test proves the
// set matches the dispatch.
//
// **Only the 1.10.1 names are declared** (ADR 0248 §4): `update`/`doctor`/`login`/`logout` and
// `prompt print` still run through the rewrite table in main.go, but a help page that listed both
// sets would be the two-name confusion the grouping exists to end.
type declarationsCatalog struct {
	specs  []*help.Spec
	topics map[string]help.Topic
}

func (c declarationsCatalog) Specs() []*help.Spec { return c.specs }

func (c declarationsCatalog) Topic(name string) (help.Topic, bool) {
	topic, ok := c.topics[name]
	return topic, ok
}

// Topics lists the topics by name, so the top-level help and an unknown-topic error both name the
// real set instead of a copy of it.
func (c declarationsCatalog) Topics() []help.Topic {
	names := make([]string, 0, len(c.topics))
	for name := range c.topics {
		names = append(names, name)
	}
	sort.Strings(names)
	topics := make([]help.Topic, 0, len(names))
	for _, name := range names {
		topics = append(topics, c.topics[name])
	}
	return topics
}

// exitsWithCredential is what a command that talks to the server can return: success, an ordinary
// failure, a usage error, and credential-unavailable.
var exitsWithCredential = []int{0, 1, 2, 4}

// exitsLocal is for a command that touches only this machine.
var exitsLocal = []int{0, 1, 2}

// exitsRefusal is for a command whose only job is to say where the action lives.
var exitsRefusal = []int{0, 1}

// keyLine is the credential path for a machine with nobody in front of it, worded once. It appears on
// the pages a person lands on when a login will not do (login, startup, help env), because a page
// that only says "see help auth" costs the reader a second command (v22 S1/A1).
//
// **It is about attendance, not about browsers.** Since v68 a browser-less machine can log in — the
// link opens anywhere — so what this sentence is for is the machine nobody is sitting at, where no
// link can be approved by definition.
const keyLine = "无人值守（CI / 容器里没有人在旁边）：控制台 /account/keys 签一把 skm_…，" +
	"放进 SKILLMASTER_API_KEY，用 skmr auth status 验证。"

// autoLoginLine is what a read command does with no credential. None of these six take `--no-login`,
// so the sentence has to say what happens instead of naming a flag they do not have (v22 A4).
const autoLoginLine = "没有凭据时会先登录；沙箱 / CI 请先设 SKILLMASTER_API_KEY（help auth）。"

// versionRuleLine is the one thing a bare address does not mean: it is the version pinned by this
// machine's last `invoke`, not whatever is live now. One string, four pages — the rule is the same
// fact wherever a read takes an address (v22 B4).
const versionRuleLine = "裸地址用上次 invoke 钉住的版本，不是当前线上；要别的版本就写 @<版本>。"

var commandHelp = declarationsCatalog{
	topics: map[string]help.Topic{
		"env": {
			Name:  "env",
			Label: "环境变量",
			Body: `环境变量：
  SKILLMASTER_SERVER       服务端地址（默认本地）
  SKILLMASTER_API_KEY      API key；优先级最高，别 echo
  SKILLMASTER_SKILLS_DIR   submit 裸名字的目录（默认 ~/.claude/skills）
  SKILLMASTER_WEB_URL      审批页地址（默认同上）
  SKILLMASTER_AUTO_UPDATE  设 0 关自动更新
  SKILLMASTER_REPORT       设 0 关守则覆盖率上报
无人值守：控制台 /account/keys 签 skm_… 填进 SKILLMASTER_API_KEY，auth status 验证。
`,
		},
		"rule": {
			Name:  "rule",
			Label: "守则",
			Body: `守则（Agent）：
  1. 先问用户写不写、写哪份记忆文件。
  2. 取正文：skmr self rule print
  3. 先备份，再写进你客户端的文件；Cursor 的 .mdc 开头要有
     frontmatter（---、description、alwaysApply: true），写完重载窗口，项目在主目录下。
  4. 回读：skmr self rule check <路径>（当前且无旧段）。
  5. 不让写：skmr self rule decline。
CLI 不写，也不给默认路径。
凭据失效：Agent 自己运行 skmr auth login。
`,
		},
		"auth": {
			Name:  "auth",
			Label: "凭据",
			Body: `两种凭据：
  登录：skmr auth login —— 打印一个链接，任意浏览器批准（手机也行，沙箱也行）。
  API key：控制台 /account/keys 签一把 skm_…，放进 SKILLMASTER_API_KEY。
           无人值守的机器用它：不用任何人同意，也不写本机。
验证：skmr auth status
续期 / 撤销：登录的去控制台「登录设备」，key 的去「API 密钥」。
auth logout 会清本机；CLI 登录还会结束服务端那次登录，key 只清本机。
`,
		},
		"workflow": {
			Name:  "workflow",
			Label: "主流程",
			Body: `  消费：skills search → skill invoke <地址> → 照正文做；子文件用 skill ls / read
        裸地址=上次 invoke 钉的版；别的版本写 @<版本>
  运行：正文让你跑脚本 → run <地址> -- python3 x.py [参数]
        在 scripts/ 下执行；首次加 --yes；绑定的凭据会注入
  创作：skill create <名> --description <描述>
        或 draft create <地址> --name <名>
        → skill read <地址>@<版本码> <路径>（覆盖前先读）
        → skill write / edit → version diff → skill check
        → 网页上线：version open <地址>@<版本码> --sub actions
  整目录：skill submit <目录> --name <名> → skills pending → 网页上线
  协作：skill share → 对方 notifications list → notifications accept / decline；skill request → notifications approve / deny
  凭据：secret set --skill <地址> --env <NAME>（值走 stdin）→ run 注入
  二进制：skill write <地址>@<版本码> <路径> --from <本地文件>
`,
		},
		"reference": {
			Name:   "reference",
			Label:  "完整参考",
			Render: help.RenderReference,
		},
	},
	specs: append(append(append(append(append(append(append(append(append(append(topSpecs, skillSpecs...),
		skillFileSpecs...), skillRoleSpecs...), skillPageSpecs...), otherSpecs...),
		draftSpecs...), notificationSpecs...), usageSpecs...), secretSpecs...), feedbackSpecs...),
}

// topSpecs is the root, the command groups, the top-level commands, and auth status.
var topSpecs = []*help.Spec{
	{
		Path:      "",
		Summary:   "SkillMaster AI 命令行",
		Children:  []string{"self", "auth", "skill", "skills", "version", "draft", "notifications", "usage", "secret", "feedback", "startup", "run", "settings"},
		PureGroup: true,
	},
	{
		Path:      "self",
		Summary:   "CLI 自身管理",
		Children:  []string{"update", "doctor", "rule"},
		PureGroup: true,
	},
	{
		Path:      "skill",
		Summary:   "单个 skill 的动词",
		Children:  []string{"invoke", "versions", "show", "license", "open", "ls", "find", "stat", "head", "tail", "read", "cat", "wc", "grep", "check", "get", "write", "edit", "rm", "mv", "cp", "submit", "create", "copy", "join", "leave", "share", "shares", "role", "unshare", "request", "tags", "tag", "category", "my-when-to-use", "restore", "delete", "offline", "online", "visibility"},
		PureGroup: true,
	},
	{
		Path:      "skills",
		Summary:   "整个库的动词",
		Children:  []string{"list", "search", "pending", "publish", "categories", "labels", "public-tags", "my-tags"},
		PureGroup: true,
	},
	{
		Path:      "version",
		Summary:   "作者面的版本管理",
		Children:  []string{"list", "show", "diff", "rename", "note", "release-type", "discard", "open", "publish", "rollback", "revoke", "restore"},
		PureGroup: true,
	},
	{
		Path:      "auth",
		Summary:   "凭据：状态、登录、退出",
		Children:  []string{"status", "login", "logout"},
		PureGroup: true,
	},
	{
		Path:    "settings",
		Summary: "账号的搜索偏好",
		Usage:   "skmr settings [--public-recall on|off] [--json]",
		Flags: []help.Flag{
			{Name: "--public-recall", Value: "on|off", Summary: "库内结果不足时是否列出公开库的匹配"},
		},
		Output: "不带参数时打印「库内结果不足时列出公开库的匹配：开启 / 关闭」。带 --public-recall 时写这个账户的设置，" +
			"并打印写入后的值；控制台「通用」页的开关改的是同一个设置。",
		JSONFields: []string{"public_recall_hint"},
		Failures: []help.Failure{
			{Code: "usage", Summary: "--public-recall 只接受 on 或 off", Next: "改成 on 或 off 后重试"},
			{Code: "unauthenticated", Summary: help.UnauthenticatedSummary, Next: help.UnauthenticatedNext},
			{Code: "insufficient_permission", Summary: "读要 skills:read，写要 skills:write",
				Next: "换一把两档都有的密钥，或去控制台「通用」改"},
		},
		Examples: []string{"skmr settings", "skmr settings --public-recall off"},
		Parent:   "",
		Next:     "skmr skills search \"<问题>\"",
		Exits:    exitsWithCredential,
	},
	{
		Path:    "auth login",
		Summary: "登录这台机器",
		Usage:   "skmr auth login [--keyring]",
		Output: "在终端打印一个一次性链接，你在任意浏览器里打开并批准——手机、另一台电脑都行，" +
			"云设备不需要自己的浏览器。批准后这条命令自动继续，通常两秒内完成；链接 10 分钟有效，" +
			"只能批准一次，不要转发。Ctrl-C 取消，会尽力通知服务端结束这次待批准；" +
			"通知不到也会在 10 分钟后失效。\n" +
			"登录成功后在 stdout 打印这份凭据是谁的、到什么时候；诊断走 stderr。\n" +
			"凭据默认写状态目录里的 0600 明文文件（仅本人可读）；--keyring 写系统钥匙串。" +
			"在沙箱里它可能被直接拒绝（不弹窗，本次回退明文文件），也可能弹一次审批后只写进会话副本" +
			"（重启后可能丢失）；输出会说明落在系统钥匙串还是明文文件。\n" +
			keyLine,
		Flags: []help.Flag{
			{Name: "--keyring", Summary: "这一次写系统钥匙串；写不进就回退明文文件"},
		},
		Failures: []help.Failure{
			{Code: "network", Summary: "打开或连不上服务端", Next: "检查网络与地址后重试"},
			{Code: "login_failed", Summary: "页面上没有批准，或链接过期了", Next: "重新运行 skmr auth login 拿一个新链接"},
			{Code: "too_many_requests", Summary: "登录试得太频繁", Next: "等一会儿再试"},
		},
		Examples: []string{"skmr auth login", "skmr auth login --keyring"},
		Parent:   "auth",
		Exits:    exitsLocal,
	},
	{
		Path:    "auth logout",
		Summary: "退出本机的凭据",
		Usage:   "skmr auth logout",
		Output: "清掉本机副本；CLI 登录还会结束服务端那次登录，" +
			"API key 只清本机并告诉你服务端那把去哪里撤销。",
		Failures: []help.Failure{
			{Code: "network", Summary: "读不出本机凭据", Next: "检查钥匙串后重试"},
		},
		Examples: []string{"skmr auth logout"},
		Parent:   "auth",
		Exits:    []int{0, 1},
	},
	{
		Path:    "startup",
		Summary: "开始入门旅程",
		Usage:   "skmr startup",
		Output: "先确保这台机器登录：没有凭据时它先带你登录——终端打印链接，你在任意浏览器里批准；" +
			"没有浏览器可用时也会说明改用 skmr auth login 或 API key 怎么继续。登录后加载官方入门技能 " +
			"skillmaster/skillmaster-startup、钉住这一版，正文原样打印到 stdout；提示走 stderr。\n" + keyLine,
		Failures: []help.Failure{
			{Code: "usage", Summary: "多给了参数", Next: "去掉多余的参数：startup 不带参数"},
			{Code: "network", Summary: "连不上服务端", Next: "检查网络与地址后重试"},
			{Code: "login_failed", Summary: "页面上没有完成登录", Next: "重新运行 skmr startup"},
			{Code: "not_found", Summary: "旅程技能不存在或对它不可见", Next: "去控制台确认它还在线上"},
		},
		Examples: []string{"skmr startup"},
		Parent:   "",
		Exits:    exitsWithCredential,
	},
	{
		Path:    "run",
		Summary: "执行 skill 命令",
		Usage: "skmr run <地址>[@版本] [--cwd <目录>] [--cache-in <目录>] [--no-cache] [--yes] " +
			"[--json] [--explain] [--with <路径>]... [-- <命令...>]",
		Flags: []help.Flag{
			{Name: "--cwd", Value: "<目录>", Summary: "在哪个目录执行；相对路径按当前目录算"},
			{Name: "--cache-in", Value: "<目录>", Summary: "取回的版本放哪；默认在临时目录里"},
			{Name: "--no-cache", Summary: "不命中缓存，取到一个新的临时目录再跑"},
			{Name: "--yes", Aliases: []string{"-y"}, Summary: "跳过首次执行的确认，并记下来"},
			{Name: "--json", Summary: "跑完在 stdout 打一行结果 JSON"},
			{Name: "--explain", Summary: "只显示生效契约与解析结果，不执行"},
			{Name: "--with", Value: "<路径>", Summary: "额外补一个这一版清单里的文件（可重复）"},
			{Name: "--", Summary: "分隔符：后面是要执行的命令（脚本路径会解析）"},
		},
		Args: []help.Arg{
			{Name: "<地址>", Key: "address"},
			{Name: "<命令...>", Key: "command"},
		},
		Output: "stdout / stderr 原样透传。" +
			"取回闭包 `files`+`path`+`pythonpath`（默认 `scripts/`），核对。" +
			"脚本按闭包解析：扩展名白名单或 `#!`；`PATH` 上的裸词不劫持。" +
			"命令可省：有 `default` 就执行。" +
			"cwd：script / package / project。" +
			"版本可写名字、sha256 或草稿码。" +
			"草稿凭据问不到时会说明并照常运行。" +
			"服务凭据注入并覆盖同名变量。",
		Failures: []help.Failure{
			{Code: "usage", Summary: "-- 后面没有命令、契约也没有 default，或给了不止一个地址", Next: "写 -- <命令>；或让作者声明 default"},
			{Code: "not_found", Summary: "地址不存在，或这个版本看不到", Next: "skmr skill versions <地址> 看有哪些版本"},
			{Code: "skill_gone", Summary: "这个 skill 已被作者删除", Next: "地址不再服务；换一个用 skmr skills search"},
			{Code: "network", Summary: "连不上服务端", Next: "先跑 skmr self doctor 看网络与沙箱"},
			{Code: "credential_quota_exceeded", Summary: "绑定的凭据今天到上限了", Next: "让用户跑 skmr secret quota；别自行提高"},
		},
		Examples: []string{"skmr run dev/pdf-tools -- python3 plan.py"},
		Parent:   "",
		Exits:    exitsWithCredential,
		ExitNote: "被执行的命令的退出码原样返回；run 自己失败时是 1 / 2 / 4，超时是 124。",
	},
	{
		Path:    "self doctor",
		Summary: "自检；--fix 改配置",
		Usage:   "skmr self doctor [--fix [--undo] [--dry-run]]",
		Output: "不带参数：只打印八行状态（「守则」一行只报检测到的客户端数与其中几个有当前守则，不打印路径、文件内容或默认路径表；末行回答「这个进程能不能直接敲 skmr」），必要时另加一段按客户端区分的可复制配置，不写任何设置。\n" +
			"--fix：运行即表示同意写入；把状态目录与 SkillMaster AI 的平台/更新域名写进检测到的客户端；" +
			"没有域名键的打开网络总开关，" +
			"Codex 旧式还会打开工作区可写（不只是状态目录）。" +
			"--undo 移除与写入值完全一致的条目（同值无法区分来源），不还原这些开关；--dry-run 只打印变更。写入失败时退出码非 0。",
		Flags: []help.Flag{
			{Name: "--fix", Summary: "把白名单写进客户端配置"},
			{Name: "--undo", Summary: "撤掉写入的条目（同值也会撤）"},
			{Name: "--dry-run", Summary: "只打印会改什么，不落盘（配合 --fix）"},
		},
		Failures: []help.Failure{
			{Code: "usage", Summary: "参数组合不对", Next: "skmr self doctor，或加 --fix"},
		},
		Examples: []string{"skmr self doctor", "skmr self doctor --fix"},
		Parent:   "self",
		Exits:    exitsLocal,
	},
	{
		Path:    "auth status",
		Summary: "这份凭据是什么",
		Usage:   "skmr auth status [<地址>]",
		Args:    []help.Arg{{Name: "[<地址>]", Key: "address"}},
		Output:  "本机存了哪种凭据、服务端认它是谁、到期时间；key 还给出续期 / 撤销地址。诊断走 stderr。",
		Failures: []help.Failure{
			{Code: "unauthenticated", Summary: help.UnauthenticatedSummary, Next: help.UnauthenticatedNext},
			{Code: "token_idle", Summary: "这份凭据闲置太久", Next: help.UnauthenticatedNext},
			{Code: "token_expired", Summary: "这份凭据已到期", Next: help.UnauthenticatedNext},
			{Code: "token_revoked", Summary: "这份凭据已被结束", Next: help.UnauthenticatedNext},
			{Code: "account_suspended", Summary: "账号已被停用", Next: "联系管理员"},
		},
		Examples: []string{"skmr auth status", "skmr auth status skillmaster/skillmaster-startup"},
		Parent:   "auth",
		Exits:    exitsWithCredential,
	},
	// **The rule verbs only read** (ADR 0249): the file belongs to the agent, so the CLI hands over
	// the text, answers one file's state, or records "stop asking". It never writes an instruction
	// file and keeps no record of one — that record is what sent a WorkBuddy user to Claude's file.
	{
		Path:      "self rule",
		Summary:   "这段守则（只读）",
		Children:  []string{"print", "check", "decline"},
		PureGroup: true,
	},
	{
		Path:    "self rule print",
		Summary: "打印守则正文",
		Usage:   "skmr self rule print",
		Output:  "把这个二进制自带的那段说明原样写到 stdout，断网、没有凭据也能用。Agent 拿到正文后写进它自己的全局记忆文件，先问用户。",
		Failures: []help.Failure{
			{Code: "usage", Summary: "多给了参数", Next: "去掉多余的参数"},
		},
		Examples: []string{"skmr self rule print"},
		Parent:   "self rule",
		Next:     "skmr self rule check <路径>",
		Exits:    exitsLocal,
	},
	{
		Path:    "self rule check",
		Summary: "看一个文件的守则状态",
		Usage:   "skmr self rule check <路径>",
		Args:    []help.Arg{{Name: "<路径>", Key: "path"}},
		Output: "读一次这个文件，不改它：当前（退出码 0）、旧版、被改过、读不到、没有、不管（都是 1）。" +
			"「读不到」是 Cursor 的 .mdc 缺 frontmatter——编辑器会忽略整个文件；「不管」是符号链接、目录或在 Git 仓库里的项目级文件；" +
			"「当前」后面若带「旁边还有一段旧守则」，先删掉那段再查。",
		Failures: []help.Failure{
			{Code: "usage", Summary: "没给路径，或给了不止一个", Next: "只给一个要检查的文件路径"},
		},
		Examples: []string{"skmr self rule check <路径>"},
		Parent:   "self rule",
		Exits:    exitsLocal,
	},
	{
		Path:    "self rule decline",
		Summary: "不再提示守则更新",
		Usage:   "skmr self rule decline",
		Output:  "记下「不再提示」：之后命令末尾不再出现守则更新提示；已写入的文件不动。",
		Failures: []help.Failure{
			{Code: "usage", Summary: "多给了参数", Next: "去掉多余的参数"},
		},
		Examples: []string{"skmr self rule decline"},
		Parent:   "self rule",
		Exits:    exitsLocal,
	},
	{
		Path:    "self update",
		Summary: "更新这个 CLI",
		Usage:   "skmr self update [--check]",
		Flags: []help.Flag{
			{Name: "--check", Summary: "只看有没有新版，不下载"},
		},
		Output: "默认下载本通道的新版本、核对 SHA-256 并换位，下一条命令生效。\n" +
			"它替换的是正在运行的二进制；沙箱里改不了名时，需要放到沙箱外跑一次。\n" +
			"平时不需要它：每次命令启动时最多每 24 小时自动检查一次。",
		Failures: []help.Failure{
			{Code: "network", Summary: "连不上发布地址", Next: "检查网络后重试"},
			{Code: "not_found", Summary: "发布源上没有这个平台的包", Next: "重跑安装说明里那条命令"},
			{Code: "invalid_request", Summary: "下载的包校验不符", Next: "重跑安装说明里那条命令"},
		},
		Examples: []string{"skmr self update --check", "skmr self update"},
		Parent:   "self",
		Exits:    exitsLocal,
	},
}
