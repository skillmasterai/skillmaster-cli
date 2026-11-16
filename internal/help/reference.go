package help

import (
	"fmt"
	"strings"
)

// RenderReference is the full command reference: every executable path, grouped by the top-level
// command it lives under, in command-tree order. It is generated from the declarations at render
// time — there is no stored second copy — and it has no budget, because it is read on purpose
// (v77, following gh's `gh help reference`).
func RenderReference(cat Catalog) string {
	root, ok := Lookup(cat, "")
	if !ok {
		return ""
	}
	total := 0
	for _, spec := range cat.Specs() {
		if spec.Path != "" && !spec.PureGroup {
			total++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "skmr 完整参考 · %d 条命令\n", total)
	seen := map[string]bool{}
	for _, top := range root.Children {
		topSpec, ok := Lookup(cat, top)
		if !ok {
			continue
		}
		lines, n := referenceLines(cat, topSpec, seen)
		if n == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s · %d 条命令\n", top, n)
		for _, line := range lines {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return b.String()
}

// referenceLines walks one top-level subtree in tree order. The usage line comes from the
// declaration, so the reference cannot drift from what the command actually accepts. seen guards
// a malformed declaration (a child pointing back at an ancestor) from recursing forever.
func referenceLines(cat Catalog, spec *Spec, seen map[string]bool) ([]string, int) {
	if seen[spec.Path] {
		return nil, 0
	}
	seen[spec.Path] = true
	var lines []string
	n := 0
	if !spec.PureGroup {
		usage := spec.Usage
		if usage == "" {
			usage = "skmr " + spec.Path
		}
		lines = append(lines, usage+"  "+spec.Summary)
		n = 1
	}
	for _, child := range spec.Children {
		childSpec, ok := Lookup(cat, joinPath(spec.Path, child))
		if !ok {
			continue
		}
		sub, subN := referenceLines(cat, childSpec, seen)
		lines = append(lines, sub...)
		n += subN
	}
	return lines, n
}
