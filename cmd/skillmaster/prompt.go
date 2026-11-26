package main

import (
	"context"
	"fmt"
	"os"

	"github.com/skillmasterai/skillmaster-cli/internal/prompt"
)

// `prompt` — the rule block that goes into an agent's instruction file.
//
// **The block is a paragraph, and `print` is the escape hatch for writing it by hand** (v94, ADR
// 0242; ADR 0110 retired the marker-based `status` / `update` / `uninstall`). `skmr install --file`
// is the normal path; `print` is what an agent uses where the CLI cannot write — a sandbox, a
// read-only mount — and the file is then registered with `install --file` like any other.
func promptVerb(ctx context.Context, args []string) error {
	_ = ctx
	if len(args) == 0 {
		return usagef("%s", usageAt("prompt"))
	}
	// Named rather than unknown, and checked before the dispatch switch: these four were in the
	// help page until v26, and a person who types one deserves to know why it stopped. They are not
	// children of `prompt` any more, so they stay out of the switch — the declared tree and the
	// dispatch have to agree (tree_test.go).
	if retiredPromptVerbs[args[0]] {
		return retiredPromptVerb(args[0])
	}
	switch args[0] {
	case "print":
		return promptPrint(args[1:])
	default:
		return unknownName("prompt", args[0], childNames("prompt"))
	}
}

// retiredPromptVerbs are the commands that wrote, located, updated or removed the copy.
var retiredPromptVerbs = map[string]bool{"install": true, "status": true, "update": true, "uninstall": true}

// retiredPromptVerb answers them. It says where the work happens now instead of only refusing:
// "no such command" for something the help page listed last week would be a worse answer.
func retiredPromptVerb(name string) error {
	// **On stderr, like every other refusal.** stdout carries results — for this group that means
	// exactly one thing, the block from `prompt print` — so `skmr prompt install > notes.md`
	// must not put prose in the file.
	fmt.Fprintf(os.Stderr, "skmr prompt %s 已经退役：写守则改用 skmr install --file <路径> --yes。\n", name)
	fmt.Fprintln(os.Stderr, "只想要正文：skmr prompt print。")
	fmt.Fprintln(os.Stderr, "给 Agent 的完整步骤：skmr help rule。")
	return &exitError{code: 2}
}

// promptPrint writes the block this binary carries, verbatim.
//
// **The manual escape hatch, and the one that works offline.** A person who would rather paste it
// themselves, or whose sandbox refuses the write, gets exactly the bytes the journey would write.
func promptPrint(args []string) error {
	if len(args) > 0 {
		return unknownFlag("prompt print", args[0])
	}
	os.Stdout.Write(prompt.Block())
	return nil
}
