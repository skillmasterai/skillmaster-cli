package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/help"
)

// The command set these tests measure against is the PRD's count of executable paths, which already
// includes the bare `skills my-tags`; the pure groups and the top level are nodes, not paths.
// v25 added `update` and the `prompt` group's four verbs, v26 added `prompt uninstall`, v26's 0005
// retired all four of that group except `print` (ADR 0110), v34 retired `files` while adding
// `ls` / `find` / `stat` / `head` / `tail` — 65 − 4 + 4 = 65. v39 added `put` and `get`: 67 + 2.
// v52 adds the eight `secret` verbs (sharing is console-only, so no share/unshare): 79 + 8.
// v54 adds `skill copy`: 90 + 1. v58 adds the `usage` verbs; v59 adds
// `skill my-when-to-use` and its two verbs; v65 adds `skills labels` and `feedback attach`: 97 + 2.
const wantCommandPaths = 102

// relativeTimeWords are the phrases a help text must not use: they describe the product's state at
// the moment somebody wrote it, and they go stale without anyone editing the text.
var relativeTimeWords = []string{"目前", "即将", "暂时", "以后", "未来"}

func commandPaths(cat help.Catalog) []*help.Spec {
	paths := make([]*help.Spec, 0, len(cat.Specs()))
	for _, spec := range cat.Specs() {
		if spec.Path != "" && !spec.PureGroup {
			paths = append(paths, spec)
		}
	}
	return paths
}

func TestDeclarationsMatchTheCommandTree(t *testing.T) {
	if err := help.Validate(commandHelp); err != nil {
		t.Fatalf("declarations do not validate: %v", err)
	}
	paths := commandPaths(commandHelp)
	if len(paths) != wantCommandPaths {
		t.Fatalf("declared %d command paths, want %d", len(paths), wantCommandPaths)
	}
	for _, spec := range paths {
		if spec.Summary == "" {
			t.Errorf("%q has no summary", spec.Path)
		}
		if n := utf8.RuneCountInString(spec.Summary); n > 12 {
			t.Errorf("%q summary is %d runes, want at most 12: %q", spec.Path, n, spec.Summary)
		}
		if spec.Usage == "" {
			t.Errorf("%q has no usage line", spec.Path)
		}
		if len(spec.Exits) == 0 {
			t.Errorf("%q declares no exit codes", spec.Path)
		}
		if len(spec.Failures) == 0 && len(spec.Children) == 0 {
			t.Errorf("%q declares no failures", spec.Path)
		}
	}
}

func TestEveryHelpFitsTheBudget(t *testing.T) {
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*m")
	for _, spec := range commandHelp.Specs() {
		text := help.Render(commandHelp, spec.Path)
		if text == "" {
			t.Errorf("%q renders nothing", spec.Path)
			continue
		}
		limit := 2048
		if spec.PureGroup && len(spec.Children) > 0 {
			// A group page grows with the command surface; a leaf does not (v77).
			limit = 3072
		}
		if len(text) > limit {
			t.Errorf("%q help is %d bytes, want at most %d", spec.Path, len(text), limit)
		}
		if ansi.MatchString(text) {
			t.Errorf("%q help has ANSI colour", spec.Path)
		}
		for _, word := range relativeTimeWords {
			if strings.Contains(text, word) {
				t.Errorf("%q help uses the relative-time word %q", spec.Path, word)
			}
		}
	}
}

func TestTopLevelHelpListsEverything(t *testing.T) {
	text := help.Render(commandHelp, "")
	for _, name := range []string{"skill", "skills", "version", "auth", "self", "startup"} {
		if !strings.Contains(text, name) {
			t.Errorf("top-level help does not mention %q", name)
		}
	}
}

// The unknown-topic line names the topics the catalog carries, so adding one is a map entry rather
// than a hunt for the places that say "env". This test adds a second topic to the real catalog,
// asks for a third, and reads the answer.
func TestAnUnknownTopicNamesTheRealSet(t *testing.T) {
	original := commandHelp
	t.Cleanup(func() { commandHelp = original })
	commandHelp = declarationsCatalog{
		specs: original.specs,
		topics: map[string]help.Topic{
			"env":  original.topics["env"],
			"auth": {Name: "auth", Label: "登录", Body: "登录主题\n"},
		},
	}

	handled, err := helpRequest([]string{"help", "nosuch"})
	if !handled || err == nil {
		t.Fatalf("an unknown topic was not refused: handled=%v err=%v", handled, err)
	}
	for _, want := range []string{"env", "auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name the topic %q", err, want)
		}
	}

	top := help.RenderTop(commandHelp)
	if !strings.Contains(top, "登录：skmr help auth") {
		t.Errorf("the top-level help does not list the second topic:\n%s", top)
	}
}

// **Completeness, not just presence.** A command that can return 4 has to say so in its own help:
// the two shared failures are rendered by the kernel, so this test fails if either the gate or the
// table goes missing — not if a declaration forgot to repeat them.
func TestEveryCommandThatCanExitFourSaysWhatToDo(t *testing.T) {
	for _, spec := range commandHelp.Specs() {
		if spec.PureGroup || spec.Path == "" {
			continue
		}
		text := help.Render(commandHelp, spec.Path)
		canFour := false
		for _, code := range spec.Exits {
			if code == 4 {
				canFour = true
			}
		}
		if !canFour {
			if strings.Contains(text, "运行 skmr auth login，或到控制台换一把 API key") {
				t.Errorf("%q never leaves this machine but advertises a credential failure:\n%s", spec.Path, text)
			}
			continue
		}
		// `skmr auth login` is the next step an agent needs; a command may say it in its own
		// words (auth status does) or get it from the shared line.
		for _, want := range []string{"skmr auth login", "连不上服务端"} {
			if !strings.Contains(text, want) {
				t.Errorf("%q can exit 4 but its help does not mention %q:\n%s", spec.Path, want, text)
			}
		}
	}
}

// v22 S3 / Q4: the next step a 401 declares and the sentence the same 401 produces must agree about
// both ways in. The declaration is what an agent reads before it ever sees a 401; a declaration that
// says only 「重新登录」 sends a key holder to a command the environment key overrides, where it
// cannot help.
func TestTheUnauthenticatedNextStepAgreesWithTheRuntimeSentence(t *testing.T) {
	// **All six**, not one: the shared row only applies where a command does not declare its own
	// `unauthenticated` (internal/help/help.go's dedup), so a command that kept the old sentence
	// would silently stop naming the key path.
	for _, path := range []string{"auth status", "skills list", "skills my-tags", "skill invoke",
		"skill grep", "skill show"} {
		spec, ok := help.Lookup(commandHelp, path)
		if !ok {
			t.Fatalf("%q has no declaration", path)
		}
		var declared string
		for _, failure := range spec.Failures {
			if failure.Code == "unauthenticated" {
				declared = failure.Next
			}
		}
		if declared == "" {
			t.Errorf("%q declares no unauthenticated next step", path)
			continue
		}
		// The declaration serves both shapes, so it names both: the login and where a key is
		// replaced.
		for _, want := range []string{"skmr auth login", "API key"} {
			if !strings.Contains(declared, want) {
				t.Errorf("%q's declared next step omits %q: %s", path, want, declared)
			}
		}
		if !strings.Contains(declared, "控制台") {
			t.Errorf("%q's declared next step names no place to replace a key: %s", path, declared)
		}
	}
	if loginSentence, _ := refusalMessage(&api.Refusal{Code: "unauthenticated"}, false); !strings.Contains(loginSentence, "skmr auth login") {
		t.Errorf("the login-credential sentence omits the login command: %s", loginSentence)
	}
	if keySentence, _ := refusalMessage(&api.Refusal{Code: "unauthenticated"}, true); !strings.Contains(keySentence, "/account/keys") {
		t.Errorf("the key sentence omits the console address: %s", keySentence)
	}
}

// groupThreshold is the point where a flat list stops being scannable: at this many verbs a pure
// group page must declare groups. The renderer does not check it — grouping is a declaration
// fact — so the test is the only place the rule lives (v77).
const groupThreshold = 20

func TestGroupedPagesFitTheThreshold(t *testing.T) {
	for _, spec := range commandHelp.Specs() {
		if !spec.PureGroup || spec.Path == "" || len(spec.Children) == 0 {
			continue
		}
		if problem := groupViolation(commandHelp, spec, groupThreshold); problem != "" {
			t.Errorf("%s: %s", spec.Path, problem)
		}
	}
}

// groupViolation returns "" when the page's grouping obeys the rules, or a sentence saying which
// one it breaks. It is a pure function so the threshold can be exercised with synthetic catalogs.
func groupViolation(cat help.Catalog, spec *help.Spec, threshold int) string {
	grouped, ungrouped := 0, 0
	seen := map[string]bool{}
	for _, child := range spec.Children {
		childSpec, ok := help.Lookup(cat, spec.Path+" "+child)
		if !ok {
			// A child with no declaration cannot be grouped; counting it as ungrouped keeps the
			// "must group at 20" rule from being bypassed by a synthetic tree.
			ungrouped++
			continue
		}
		if childSpec.Group == "" {
			ungrouped++
			continue
		}
		grouped++
		seen[childSpec.Group] = true
	}
	if grouped > 0 && ungrouped > 0 {
		return "mixes grouped and ungrouped verbs"
	}
	if len(spec.Children) >= threshold && grouped == 0 {
		return fmt.Sprintf("has %d verbs but no groups; at %d it must group", len(spec.Children), threshold)
	}
	if grouped > 0 && len(seen) < 2 {
		return "has a single group; a group header for one group adds lines without adding structure"
	}
	return ""
}

// specCatalog is the minimum Catalog a threshold test needs: a set of specs and no topics.
type specCatalog []*help.Spec

func (c specCatalog) Specs() []*help.Spec             { return c }
func (c specCatalog) Topic(string) (help.Topic, bool) { return help.Topic{}, false }
func (c specCatalog) Topics() []help.Topic            { return nil }

func TestGroupThresholdRules(t *testing.T) {
	build := func(n int, groupOf func(int) string) (specCatalog, *help.Spec) {
		parent := &help.Spec{Path: "skill", Summary: "单个 skill 的动词", PureGroup: true}
		specs := specCatalog{parent}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("v%02d", i)
			parent.Children = append(parent.Children, name)
			specs = append(specs, &help.Spec{Path: "skill " + name, Summary: "动作", Parent: "skill", Group: groupOf(i)})
		}
		return specs, parent
	}
	cases := []struct {
		name  string
		n     int
		group func(int) string
		want  string
	}{
		{"20 ungrouped fails", 20, func(int) string { return "" }, "must group"},
		{"19 ungrouped passes", 19, func(int) string { return "" }, ""},
		{"mixed fails", 20, func(i int) string {
			if i%2 == 0 {
				return "甲"
			}
			return ""
		}, "mixes"},
		{"two groups passes", 20, func(i int) string {
			if i%2 == 0 {
				return "甲"
			}
			return "乙"
		}, ""},
		{"one group fails", 20, func(int) string { return "甲" }, "single group"},
	}
	for _, tc := range cases {
		cat, spec := build(tc.n, tc.group)
		got := groupViolation(cat, spec, groupThreshold)
		if tc.want == "" && got != "" {
			t.Errorf("%s: got %q, want none", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGroupPagesStateTheirSize(t *testing.T) {
	for _, spec := range commandHelp.Specs() {
		if !spec.PureGroup || spec.Path == "" || len(spec.Children) == 0 {
			continue
		}
		text := help.Render(commandHelp, spec.Path)
		first, _, _ := strings.Cut(text, "\n")
		if !strings.Contains(first, fmt.Sprintf("共 %d 个动作", len(spec.Children))) {
			t.Errorf("%q's first line does not state its size: %q", spec.Path, first)
		}
	}
	top := help.RenderTop(commandHelp)
	root, _ := help.Lookup(commandHelp, "")
	first, _, _ := strings.Cut(top, "\n")
	if !strings.Contains(first, fmt.Sprintf("共 %d 个顶层项", len(root.Children))) {
		t.Errorf("the top-level first line does not state its size: %q", first)
	}
}

// sixGroups is the spec's grouping table, written out here so the test does not derive its
// expectation from the same Group field it is guarding (v77 technical-design §接口 1).
var sixGroups = map[string][]string{
	"读与用":          {"invoke", "versions", "show", "license", "open"},
	"文件":           {"ls", "find", "stat", "head", "tail", "read", "cat", "wc", "grep", "check", "get"},
	"写与提交":         {"write", "edit", "rm", "mv", "cp", "submit"},
	"库与共享":         {"create", "copy", "join", "leave", "share", "shares", "role", "unshare", "request"},
	"分类、标签与我的适用场景": {"tags", "tag", "category", "my-when-to-use"},
	"生命周期":         {"restore", "delete", "offline", "online", "visibility"},
}

var sixGroupOrder = []string{"读与用", "文件", "写与提交", "库与共享", "分类、标签与我的适用场景", "生命周期"}

func TestGroupedPagesNameTheirGroups(t *testing.T) {
	text := help.Render(commandHelp, "skill")
	first, _, _ := strings.Cut(text, "\n")
	total := 0
	var parts []string
	for _, group := range sixGroupOrder {
		total += len(sixGroups[group])
		parts = append(parts, fmt.Sprintf("%s %d", group, len(sixGroups[group])))
		if !strings.Contains(text, fmt.Sprintf("%s (%d)", group, len(sixGroups[group]))) {
			t.Errorf("the page does not head group %q with (%d)", group, len(sixGroups[group]))
		}
	}
	wantFirst := fmt.Sprintf("单个 skill 的动词 · 共 %d 个动作，%d 组：%s", total, len(sixGroupOrder), strings.Join(parts, " · "))
	if first != wantFirst {
		t.Errorf("the first line = %q, want %q", first, wantFirst)
	}
	order, members := renderedGroups(text)
	if !equal(order, sixGroupOrder) {
		t.Errorf("rendered group order = %v, want %v", order, sixGroupOrder)
	}
	for _, group := range sixGroupOrder {
		if !equal(members[group], sixGroups[group]) {
			t.Errorf("group %q = %v, want %v", group, members[group], sixGroups[group])
		}
	}
}

// renderedGroups parses a grouped page into its group order and members: a line at column 0 ending
// in "(n)" is a group header; the indented lines after it are its verbs.
func renderedGroups(text string) (order []string, members map[string][]string) {
	members = map[string][]string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  ") {
			fields := strings.Fields(line)
			if len(fields) > 0 && len(order) > 0 {
				members[order[len(order)-1]] = append(members[order[len(order)-1]], fields[0])
			}
			continue
		}
		if strings.HasSuffix(line, ")") {
			if at := strings.LastIndex(line, " ("); at > 0 {
				order = append(order, line[:at])
			}
		}
	}
	return order, members
}

// TestExitLinesAreRunnable checks the page's own exits resolve: the detail line to the verb's own
// command, and the reference line to the reference topic (v77).
func TestExitLinesAreRunnable(t *testing.T) {
	for _, spec := range commandHelp.Specs() {
		if !spec.PureGroup || spec.Path == "" || len(spec.Children) == 0 || !isGrouped(spec) {
			continue
		}
		text := help.Render(commandHelp, spec.Path)
		child := spec.Children[0]
		detail, reference := false, false
		for _, line := range strings.Split(text, "\n") {
			switch {
			case strings.HasPrefix(line, "逐条说明："):
				detail = true
				if !strings.Contains(line, "<子命令>") {
					t.Errorf("%s: the detail exit does not name a verb placeholder: %q", spec.Path, line)
				}
				command := strings.ReplaceAll(strings.TrimPrefix(line, "逐条说明："), "<子命令>", child)
				args := strings.Fields(strings.TrimPrefix(command, "skmr "))
				got := help.Requested(commandHelp, args)
				if got.Kind != help.KindCommand || got.Path != spec.Path+" "+child {
					t.Errorf("%s: %q resolves to %+v, want command %q", spec.Path, command, got, spec.Path+" "+child)
				}
			case strings.HasPrefix(line, "完整参考："):
				reference = true
				command := strings.TrimPrefix(line, "完整参考：")
				args := strings.Fields(strings.TrimPrefix(command, "skmr "))
				if got := help.Requested(commandHelp, args); got.Kind != help.KindTopic || got.Topic != "reference" {
					t.Errorf("%s: %q does not resolve to the reference topic: %+v", spec.Path, command, got)
				}
			}
		}
		if !detail || !reference {
			t.Errorf("%s: the page does not carry both exits", spec.Path)
		}
	}
	// An ungrouped page keeps the v16 shape plus the count line: no exits.
	for _, spec := range commandHelp.Specs() {
		if !spec.PureGroup || spec.Path == "" || len(spec.Children) == 0 || isGrouped(spec) {
			continue
		}
		text := help.Render(commandHelp, spec.Path)
		if strings.Contains(text, "逐条说明：") || strings.Contains(text, "完整参考：") {
			t.Errorf("%s is not grouped but carries the exits", spec.Path)
		}
	}
}

// TestReferenceStructure pins the generated reference's sections to the command tree: each
// top-level section is headed by its name and executable count, in tree order (v77).
func TestReferenceStructure(t *testing.T) {
	text, ok := help.RenderTopic(commandHelp, "reference")
	if !ok {
		t.Fatal("the reference topic is not registered")
	}
	root, _ := help.Lookup(commandHelp, "")
	var wantOrder []string
	for _, top := range root.Children {
		topSpec, ok := help.Lookup(commandHelp, top)
		if !ok {
			continue
		}
		want := countExecutable(topSpec)
		if want == 0 {
			continue
		}
		wantOrder = append(wantOrder, top)
		if !strings.Contains(text, fmt.Sprintf("\n%s · %d 条命令\n", top, want)) {
			t.Errorf("the reference does not head %q with %d commands", top, want)
		}
	}
	// Section headers appear after the first line, at column 0, in tree order.
	body := text[strings.Index(text, "\n")+1:]
	var gotOrder []string
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		if at := strings.LastIndex(line, " · "); at > 0 && strings.HasSuffix(line, " 条命令") {
			gotOrder = append(gotOrder, line[:at])
		}
	}
	if !equal(gotOrder, wantOrder) {
		t.Errorf("reference section order = %v, want %v", gotOrder, wantOrder)
	}
}

func countExecutable(spec *help.Spec) int {
	return countExecutableSeen(spec, map[string]bool{})
}

func countExecutableSeen(spec *help.Spec, seen map[string]bool) int {
	if seen[spec.Path] {
		return 0
	}
	seen[spec.Path] = true
	n := 0
	if !spec.PureGroup {
		n = 1
	}
	for _, child := range spec.Children {
		if childSpec, ok := help.Lookup(commandHelp, spec.Path+" "+child); ok {
			n += countExecutableSeen(childSpec, seen)
		}
	}
	return n
}

// TestGroupedChildrenAgreeWithDeclarations checks the verb set: a verb declared but not rendered,
// or rendered but not declared, fails here. Group membership is guarded separately by
// TestGroupedPagesNameTheirGroups (v77).
func TestGroupedChildrenAgreeWithDeclarations(t *testing.T) {
	for _, spec := range commandHelp.Specs() {
		if spec.Path == "" || len(spec.Children) == 0 || !isGrouped(spec) {
			continue
		}
		text := help.Render(commandHelp, spec.Path)
		got := listedChildren(text)
		want := append([]string(nil), spec.Children...)
		sort.Strings(got)
		sort.Strings(want)
		if !equal(got, want) {
			t.Errorf("%q renders %v, declares %v", spec.Path, got, want)
		}
	}
}

// TestReferenceCoversEveryCommand pins the generated reference to the declarations: every
// executable path appears with its own usage line, so adding a command cannot leave the reference
// behind (v77).
func TestReferenceCoversEveryCommand(t *testing.T) {
	text, ok := help.RenderTopic(commandHelp, "reference")
	if !ok {
		t.Fatal("the reference topic is not registered")
	}
	if !strings.Contains(text, fmt.Sprintf(" %d 条命令", wantCommandPaths)) {
		t.Errorf("the reference does not state the command count:\n%s", firstLine(text))
	}
	for _, spec := range commandPaths(commandHelp) {
		usage := spec.Usage
		if usage == "" {
			usage = "skmr " + spec.Path
		}
		if !strings.Contains(text, usage) {
			t.Errorf("the reference does not cover %q", spec.Path)
		}
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
