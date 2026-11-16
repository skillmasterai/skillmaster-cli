package help

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Render returns the help text for one command path.
//
// The layout is the contract the conformance test parses (v16 technical-design §接口): a summary
// line, then labelled sections. A command with children also gets a "子命令：" block, which is
// terminated by a blank line. A pure group has only four items — description, usage, block, parent —
// because it does not run, has no output, and cannot fail on its own.
func Render(cat Catalog, path string) string {
	spec, ok := Lookup(cat, path)
	if !ok {
		return ""
	}
	var b strings.Builder

	writeHeading(&b, cat, spec)
	fmt.Fprintf(&b, "用法：%s\n", usageLine(spec))
	if spec.Note != "" {
		fmt.Fprintf(&b, "%s\n", spec.Note)
	}

	if len(spec.Children) > 0 {
		writeChildren(&b, cat, spec)
	}

	if flags := append(append([]Flag{}, spec.Flags...), Universal(cat, path)...); len(flags) > 0 && !isPureGroup(spec) {
		b.WriteString("\n参数：\n")
		for _, flag := range flags {
			fmt.Fprintf(&b, "  %-18s %s\n", flagSpelling(flag), flag.Summary)
		}
	}

	if spec.Output != "" {
		fmt.Fprintf(&b, "\n输出：%s\n", spec.Output)
	}
	if len(spec.JSONFields) > 0 {
		fmt.Fprintf(&b, "字段：%s\n", strings.Join(spec.JSONFields, ", "))
		// Required of every --json command by 做 #2: which stream carries what. It is the same
		// sentence everywhere, so it is written here once rather than in twelve declarations.
		b.WriteString("正文走 stdout，诊断走 stderr。\n")
	}

	failures := append(append([]Failure(nil), spec.Failures...), UniversalFailures(cat, path)...)
	if len(failures) > 0 {
		b.WriteString("\n失败：\n")
		for _, failure := range failures {
			// **The sentence, not the machine's name for it.** `content_already_exists` is what
			// `--json` reports and what the registry keys on; a reader here is asking what went
			// wrong and what to do, and twenty characters of identifier per line is what makes a
			// help too long to read (v16: necessary information, no more).
			fmt.Fprintf(&b, "  %s。下一步：%s\n", failure.Summary, failure.Next)
		}
	}

	if len(spec.Examples) > 0 {
		b.WriteString("\n例子：\n")
		for _, example := range spec.Examples {
			fmt.Fprintf(&b, "  %s\n", example)
		}
	}

	fmt.Fprintf(&b, "\n上级：%s\n", parentLine(spec))
	if spec.Next != "" {
		fmt.Fprintf(&b, "下一步：%s\n", spec.Next)
	}
	// A grouped page names the two places with the details: each verb's own page, and the
	// generated full reference (v77). An ungrouped page keeps the v16 shape plus the count line.
	if spec.PureGroup && len(spec.Children) > 0 && spec.Path != "" && len(groupOrder(cat, spec)) > 0 && allGrouped(cat, spec) {
		fmt.Fprintf(&b, "逐条说明：skmr %s <子命令> --help\n", spec.Path)
		b.WriteString("完整参考：skmr help reference\n")
	}

	if !isPureGroup(spec) {
		if spec.ExitNote != "" {
			fmt.Fprintf(&b, "\n退出码：%s\n", spec.ExitNote)
		} else if len(spec.Exits) > 0 {
			fmt.Fprintf(&b, "\n退出码：%s\n", exitLine(spec.Exits))
		}
	}
	return b.String()
}

// writeHeading renders the first line. A pure group with children states how many verbs it holds,
// and — when its declarations carry Group — how many in each. The numbers come from the
// declarations, never from a hand-written list (v77): adding a verb changes the line by itself.
func writeHeading(b *strings.Builder, cat Catalog, spec *Spec) {
	if spec.PureGroup && len(spec.Children) > 0 {
		fmt.Fprintf(b, "%s · 共 %d 个动作", spec.Summary, len(spec.Children))
		groups := groupOrder(cat, spec)
		if len(groups) > 0 && allGrouped(cat, spec) {
			parts := make([]string, 0, len(groups))
			for _, group := range groups {
				parts = append(parts, fmt.Sprintf("%s %d", group, countGroup(cat, spec, group)))
			}
			fmt.Fprintf(b, "，%d 组：%s", len(groups), strings.Join(parts, " · "))
		}
		b.WriteString("\n\n")
		return
	}
	fmt.Fprintf(b, "%s\n\n", spec.Summary)
}

// writeChildren renders the verb list. Declarations that carry Group get group headers (v77);
// declarations that do not keep the v16 "子命令：" block byte for byte.
func writeChildren(b *strings.Builder, cat Catalog, spec *Spec) {
	width := 0
	for _, child := range spec.Children {
		if n := utf8.RuneCountInString(child); n > width {
			width = n
		}
	}
	groups := groupOrder(cat, spec)
	// A page with any ungrouped verb falls back to the flat block: rendering only the grouped
	// ones would make the missing verbs disappear while the count line still counts them.
	if len(groups) == 0 || !allGrouped(cat, spec) {
		b.WriteString("\n子命令：\n")
		for _, child := range spec.Children {
			childSpec, ok := Lookup(cat, joinPath(spec.Path, child))
			if !ok {
				continue
			}
			fmt.Fprintf(b, "  %-*s  %s\n", width, child, childSpec.Summary)
		}
		return
	}
	for _, group := range groups {
		fmt.Fprintf(b, "\n%s (%d)\n", group, countGroup(cat, spec, group))
		for _, child := range spec.Children {
			childSpec, ok := Lookup(cat, joinPath(spec.Path, child))
			if !ok || childSpec.Group != group {
				continue
			}
			fmt.Fprintf(b, "  %-*s  %s\n", width, child, childSpec.Summary)
		}
	}
}

// groupOrder is the order the groups are rendered in: the order each label first appears in the
// parent's Children. An empty result means the page is not grouped.
func groupOrder(cat Catalog, spec *Spec) []string {
	seen := make(map[string]bool, len(spec.Children))
	var order []string
	for _, child := range spec.Children {
		childSpec, ok := Lookup(cat, joinPath(spec.Path, child))
		if !ok || childSpec.Group == "" || seen[childSpec.Group] {
			continue
		}
		seen[childSpec.Group] = true
		order = append(order, childSpec.Group)
	}
	return order
}

// allGrouped reports whether every child carries a Group. A mixed page renders flat, so no verb
// can vanish from the list while the count line still counts it.
func allGrouped(cat Catalog, spec *Spec) bool {
	for _, child := range spec.Children {
		childSpec, ok := Lookup(cat, joinPath(spec.Path, child))
		if !ok || childSpec.Group == "" {
			return false
		}
	}
	return true
}

func countGroup(cat Catalog, spec *Spec, group string) int {
	n := 0
	for _, child := range spec.Children {
		childSpec, ok := Lookup(cat, joinPath(spec.Path, child))
		if ok && childSpec.Group == group {
			n++
		}
	}
	return n
}

// RenderTopic returns a topic's page. A topic with Render generates it from the catalog; every
// other topic reads its Body. The kernel does not know which topic is generated (v77).
func RenderTopic(cat Catalog, name string) (string, bool) {
	topic, ok := cat.Topic(name)
	if !ok {
		return "", false
	}
	if topic.Render != nil {
		return topic.Render(cat), true
	}
	return topic.Body, true
}

// isPureGroup reads the declaration's own marker. It is not inferred from the other fields: a
// seven-item body and an empty one look alike from the outside, and `skills my-tags` is executable
// with almost no items.
func isPureGroup(spec *Spec) bool {
	return spec.PureGroup
}

func usageLine(spec *Spec) string {
	if spec.Usage != "" {
		return spec.Usage
	}
	if len(spec.Children) > 0 {
		return "skmr " + spec.Path + " <子命令> [参数]"
	}
	return "skmr " + spec.Path
}

func parentLine(spec *Spec) string {
	if spec.Parent == "" {
		return "skmr help"
	}
	return "skmr " + spec.Parent + " --help"
}

func flagSpelling(flag Flag) string {
	spelling := flag.Name
	for _, alias := range flag.Aliases {
		spelling += ", " + alias
	}
	switch {
	case flag.Value == "":
		return spelling
	case flag.Sep == "=":
		return spelling + "=" + flag.Value
	default:
		return spelling + " " + flag.Value
	}
}

// exitLine is this command's own statuses, and only the numbers: the four classes and what each one
// means are written once in the top-level help, and repeating them on all fifty-six commands is
// fifty-six copies of the same sentence.
func exitLine(exits []int) string {
	parts := make([]string, 0, len(exits))
	for _, exit := range exits {
		parts = append(parts, strconv.Itoa(exit))
	}
	return strings.Join(parts, " · ")
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + " " + child
}

// RenderTop returns the top-level help. It is deliberately not a command body: it lists every
// top-level name with its help address, points at every topic the catalog carries, and shows the
// four exit classes one per line.
func RenderTop(cat Catalog) string {
	root, ok := Lookup(cat, "")
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · 共 %d 个顶层项\n\n", root.Summary, len(root.Children))
	b.WriteString("用法：skmr <命令> [参数]\n\n命令：\n")
	width := 0
	for _, child := range root.Children {
		if n := utf8.RuneCountInString(child); n > width {
			width = n
		}
	}
	for _, child := range root.Children {
		childSpec, ok := Lookup(cat, child)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  %-*s %s（%s --help）\n", width, child, childSpec.Summary, child)
	}
	for _, topic := range cat.Topics() {
		label := topic.Label
		if label == "" {
			// A topic without a label still gets a line rather than a dangling "：".
			label = topic.Name
		}
		fmt.Fprintf(&b, "\n%s：skmr help %s\n", label, topic.Name)
	}
	b.WriteString("\n退出码：\n")
	for _, class := range exitClasses {
		fmt.Fprintf(&b, "  %d %s\n", class.Code, class.Text)
	}
	return b.String()
}
