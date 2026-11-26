// Package rule installs the SkillMaster 守则 into an agent's user-level instruction file.
//
// **This package writes a file the person owns** (ADR 0242, superseding ADR 0110). The write is an
// explicit command — `skmr install --file <path>` — and the target comes only from that flag: the
// internal table in agents.go is for detection and reporting, never for choosing where to write.
package rule

import (
	"bytes"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
	"github.com/skillmasterai/skillmaster-cli/internal/prompt"
)

// legacyCommand is the old command name the historical blocks spell out. Composing it keeps the
// texts byte-identical without a second literal of the retired name (ADR 0113).
const legacyCommand = config.LegacyName

// Version names the rule text, separately from the CLI version.
//
// **It changes only when the text changes in substance** (ADR 0242): a typo or a rephrasing is not a
// new rule, and bumping it would trigger a migration for nothing. Detection, migration and the one
// notice all hang off this number.
const Version = 2

// Block is the rule text this binary carries, ending in a newline.
func Block() []byte { return prompt.Block() }

// Digest is the content digest of Block.
func Digest() string { return prompt.Digest() }

// legacyBlocks are the exact texts earlier versions wrote.
//
// **Exact text, not a prefix or a regex.** A person's instruction file is theirs; a fuzzy match would
// rewrite a sentence they deliberately changed. Anything not byte-identical is left alone and shown as
// modified by `install --check`.
var legacyBlocks = [][]byte{
	// v42–v92: the released one-line form. **This is the one most machines have** — it is what
	// cli/v1.6.0 through cli/v1.8.0 wrote. It has no "AI" and no 「（技能库入口）」.
	[]byte("**铁律：任何任务都先调用 SkillMaster 这个 skill，照它的要求做；拿不准就调用，不要自己猜。别因为「这次很简单」「手边有现成工具」「刚才已经加载过」跳过。**\n"),
	// v93's renamed form. It never shipped in a release, but a branch build may have written it.
	[]byte("**铁律：任何任务都先调用 SkillMaster AI 这个 skill（技能库入口），照它的要求做；拿不准就调用，不要自己猜。别因为「这次很简单」「手边有现成工具」「刚才已经加载过」跳过。**\n"),
	// v25–v26: the first form, between HTML markers. **This is the only legacy text the CLI itself
	// ever wrote through `prompt install`**, so it is what `prompt.json` seeding exists for: those
	// records can only point at files this block was written into.
	[]byte("<!-- " + legacyCommand + ":begin -->\n" +
		"SkillMaster 管我的全部 skill：查找、调用、编辑、整理，都通过 " + legacyCommand + " CLI 做。\n" +
		"要按某个外部系统的规矩做事、要产出有固定格式的东西、你正要凭记忆写多步流程，或用户让你按我们的做法做时，先用它；拿不准就查一次。\n" +
		"先 " + legacyCommand + " skills search \"<一句话说明要做的事>\"；结果的第二行写着每个 skill 什么时候适用。没命中换种说法，仍没有加 --scope public；用 skill invoke <地址> 加载；都没有就直说没找到；更多命令见 " + legacyCommand + " help。\n" +
		"<!-- " + legacyCommand + ":end -->\n"),
	// v27–v28: the printed form of "check before anything", still naming `skillmaster`.
	[]byte("**铁律：除了闲聊（打招呼、情绪、没有任务的对话），任何任务都先查 skill，查到先报备再调用。拿不准算不算闲聊时，当作任务去查。**\n\n" +
		"SkillMaster 管我的全部 skill：查找、调用、编辑、整理，都通过 " + legacyCommand + " CLI 做。\n\n" +
		"1. 执行前先 `" + legacyCommand + " skills search \"<一句话要做的事>\"`。不判断「拿不拿得准」，也不因为「一次性的」「手边有现成工具」跳过。默认只搜自己的库，只有用户明确说「搜公开库」时才加 `--scope public`；搜到的公开 skill 加载前先问用户。\n" +
		"2. 有相关的 skill 就 `skill invoke <地址>`。**调之前先说清用哪个 skill（地址＋拿它做什么），说完再调**，不许先调后说。没命中换说法再查一次，两次都没有才自己做，并明说「没找到相关 skill」。\n" +
		"3. 本地 / 内置列表不完整，电脑上的旧副本也不是来源，都不能当「没有」的依据。\n" +
		"4. 改 skill 在线做：`skill write <地址>@new <路径> --name <版名>`，新建用 `skill create`；上线由人在网页上点。\n" +
		"5. 其他命令见 `" + legacyCommand + " help`。\n"),
	// v29–v38: the same text after the command was renamed to `skmr`.
	[]byte("**铁律：除了闲聊（打招呼、情绪、没有任务的对话），任何任务都先查 skill，查到先报备再调用。拿不准算不算闲聊时，当作任务去查。**\n\n" +
		"SkillMaster 管我的全部 skill：查找、调用、编辑、整理，都通过 skmr CLI 做。\n\n" +
		"1. 执行前先 `skmr skills search \"<一句话要做的事>\"`。不判断「拿不拿得准」，也不因为「一次性的」「手边有现成工具」跳过。默认只搜自己的库，只有用户明确说「搜公开库」时才加 `--scope public`；搜到的公开 skill 加载前先问用户。\n" +
		"2. 有相关的 skill 就 `skill invoke <地址>`。**调之前先说清用哪个 skill（地址＋拿它做什么），说完再调**，不许先调后说。没命中换说法再查一次，两次都没有才自己做，并明说「没找到相关 skill」。\n" +
		"3. 本地 / 内置列表不完整，电脑上的旧副本也不是来源，都不能当「没有」的依据。\n" +
		"4. 改 skill 在线做：`skill write <地址>@new <路径> --name <版名>`，新建用 `skill create`；上线由人在网页上点。\n" +
		"5. 其他命令见 `skmr help`。\n"),
	// v39–v41: item 4 gained `skill put` for binary files.
	[]byte("**铁律：除了闲聊（打招呼、情绪、没有任务的对话），任何任务都先查 skill，查到先报备再调用。拿不准算不算闲聊时，当作任务去查。**\n\n" +
		"SkillMaster 管我的全部 skill：查找、调用、编辑、整理，都通过 skmr CLI 做。\n\n" +
		"1. 执行前先 `skmr skills search \"<一句话要做的事>\"`。不判断「拿不拿得准」，也不因为「一次性的」「手边有现成工具」跳过。默认只搜自己的库，只有用户明确说「搜公开库」时才加 `--scope public`；搜到的公开 skill 加载前先问用户。\n" +
		"2. 有相关的 skill 就 `skill invoke <地址>`。**调之前先说清用哪个 skill（地址＋拿它做什么），说完再调**，不许先调后说。没命中换说法再查一次，两次都没有才自己做，并明说「没找到相关 skill」。\n" +
		"3. 本地 / 内置列表不完整，电脑上的旧副本也不是来源，都不能当「没有」的依据。\n" +
		"4. 改 skill 在线做：文本用 `skill write <地址>@new <路径> --name <版名>`，图片 / PDF 等二进制用 `skill put <地址>@<版本码> <本地文件> <远端路径>`（`put` 只写已有草稿）；新建用 `skill create`；上线由人在网页上点。\n" +
		"5. 其他命令见 `skmr help`。\n"),
}

// legacySpanOutside returns the span of a legacy block that does not overlap the current rule.
//
// **A newer rule can contain an older one as a prefix** — the next version that only appends a line
// would otherwise be "cleaned" out of itself, leaving the appended tail and deleting the rule
// (found on a real update, 2026-11-26). Only a match lying entirely outside the current block
// counts as a leftover.
func legacySpanOutside(content, current []byte) (start, end int, ok bool) {
	// **Every occurrence of the current rule is protected, not just the first.** A file with the
	// rule written twice would otherwise let a match inside the second copy count as a leftover and
	// cut it open — the same failure as the prefix bug, one copy later.
	var curSpans [][2]int
	for _, variant := range lineEndingVariants(current) {
		for off := 0; ; {
			i := bytes.Index(content[off:], variant)
			if i < 0 {
				break
			}
			curSpans = append(curSpans, [2]int{off + i, off + i + len(variant)})
			off += i + 1
		}
	}
	overlapsCurrent := func(s, e int) bool {
		for _, c := range curSpans {
			if s < c[1] && e > c[0] {
				return true
			}
		}
		return false
	}
	// **The earliest leftover wins.** The new rule goes where the first old one was, so the file
	// reads in the order the person wrote it rather than the order of this table.
	best, bestEnd := -1, -1
	for _, old := range legacyBlocks {
		for _, variant := range lineEndingVariants(old) {
			for off := 0; ; {
				i := bytes.Index(content[off:], variant)
				if i < 0 {
					break
				}
				s, e := off+i, off+i+len(variant)
				// **At the same start, the longest match wins.** If one old text were a prefix of
				// another, keeping the short one would delete the prefix and leave the tail, which
				// nothing would match again.
				if !overlapsCurrent(s, e) && (best < 0 || s < best || (s == best && e > bestEnd)) {
					best, bestEnd = s, e
				}
				off = s + 1
			}
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestEnd, true
}

// headingOutsideLegacy reports whether the rule heading appears in text that is not an exact old
// block — the person's own edit.
//
// **The heading alone proves nothing.** Every rule text this project ever wrote starts with it,
// including the old blocks a migration is supposed to replace; asking `bytes.Contains` would call
// an untouched old rule "edited" and refuse the very upgrade it exists to perform (found by
// building 3.0.1 against 3.0.0, 2026-11-26). Strip the exact old blocks first, then look.
func headingOutsideLegacy(content []byte) bool {
	// **The current rule is not an edit.** Callers that already handled it never get here, but the
	// contract has to hold on its own: a later caller must not read "has the heading" as "the
	// person changed something".
	if containsBlock(content) {
		return false
	}
	rest, _ := stripLegacyOutside(content, Block())
	return bytes.Contains(rest, []byte("SkillMaster 守则："))
}

// adaptBlock returns block in the line ending the surrounding text uses, so a CRLF file stays CRLF.
func adaptBlock(block, surrounding []byte) []byte {
	if bytes.Equal(lineEnding(surrounding), []byte("\r\n")) {
		return bytes.ReplaceAll(block, []byte("\n"), []byte("\r\n"))
	}
	return block
}

// lineEnding is the convention the text ends with, and the one a separator should follow.
//
// **The tail decides, not "does this file contain a CRLF anywhere".** A file may carry a pasted
// Unix fragment in the middle and still end Windows-style; anything else writes the separator in a
// different convention from the line it lands after (found in the append path, 2026-11-26).
func lineEnding(content []byte) []byte {
	if bytes.HasSuffix(content, []byte("\r\n")) {
		return []byte("\r\n")
	}
	if bytes.HasSuffix(content, []byte("\n")) {
		return []byte("\n")
	}
	if bytes.Contains(content, []byte("\r\n")) {
		return []byte("\r\n")
	}
	return []byte("\n")
}

// stripLegacyOutside removes every old block that sits entirely outside the current rule and
// reports how many it removed.
//
// **One pass is not enough.** A file can hold two: `prompt print` appended to an existing file, and
// an uninstall that left the append behind, then the documented fallback pasted another. Removing
// only the first leaves the agent reading two rule sets — the state this function exists to end.
func stripLegacyOutside(content, current []byte) ([]byte, int) {
	removed := 0
	for {
		start, end, ok := legacySpanOutside(content, current)
		if !ok {
			return content, removed
		}
		content = splice(content, start, end, nil)
		removed++
	}
}

// legacyBlockIndex returns the exact legacy block present in content, if any.
//
// **Both line endings are tried.** A Windows machine's instruction file is CRLF; the embedded texts
// are LF, and a byte comparison would miss every old block on those machines.
func legacyBlockIndex(content []byte) []byte {
	for _, old := range legacyBlocks {
		for _, variant := range lineEndingVariants(old) {
			if bytes.Contains(content, variant) {
				return variant
			}
		}
	}
	return nil
}

// containsBlock reports whether content carries the current rule, in either line ending.
func containsBlock(content []byte) bool {
	for _, variant := range lineEndingVariants(Block()) {
		if bytes.Contains(content, variant) {
			return true
		}
	}
	return false
}

func lineEndingVariants(text []byte) [][]byte {
	return [][]byte{text, bytes.ReplaceAll(text, []byte("\n"), []byte("\r\n"))}
}
