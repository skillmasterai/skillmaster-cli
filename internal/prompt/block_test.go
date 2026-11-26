package prompt

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/config"
)

func TestTheBlockIsTheApprovedText(t *testing.T) {
	block := string(Block())
	for _, want := range []string{
		// **The heading is the name the product uses** (2026-11-25): 「铁律」 was too strong for
		// something a person can decline, and 「规则」 was too generic in a prompt file that also
		// carries other rules. 守则 is the approved name.
		"SkillMaster 守则：",
		// **Privacy gates the first line, not the last.** The N=50 evaluation with privacy as the
		// final sentence searched before asking in 5 of 6 cases; putting consent first raised full
		// compliance from 1/6 to 4/6 (PII never entered a search command in either run).
		"涉及个人隐私的查询，先问用户是否同意；同意后再搜，不要先搜。",
		// **CLI-first**: the rule names the two commands an agent needs, without a skill to load.
		"开始任何任务前，先跑 `skmr skills search \"<任务一句话>\"`；没有查到就按常规继续。",
		"要新建/修改/发布/共享/删除 skill 时，先跑 `skmr help` 看怎么做。",
		// **The exception stays explicit** — without it the rule fires on small talk.
		"闲聊、常识问答和简单计算/翻译不用。",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not contain %q", want)
		}
	}
	if !strings.HasSuffix(block, "\n") {
		t.Error("the block does not end in a newline")
	}
	// **Any occurrence, not just one followed by a space.** The guard test learned this the hard way:
	// `升级 skillmaster。` has no space after the name, and that is exactly how it would appear in a
	// Chinese sentence.
	if strings.Contains(block, config.LegacyName) {
		t.Errorf("the block still names the old command %q", config.LegacyName)
	}
	// **No mark-up** (ADR 0110): the paragraph is the person's, not a machine-managed region.
	for _, unwanted := range []string{"<!-- skillmaster:begin -->", "<!-- skillmaster:end -->", "<!--"} {
		if strings.Contains(block, unwanted) {
			t.Errorf("the block still carries %q", unwanted)
		}
	}
	if Digest() != DigestOf(Block()) {
		t.Error("Digest is not the digest of Block")
	}
	if !strings.HasPrefix(Digest(), "sha256:") || len(Digest()) != len("sha256:")+64 {
		t.Errorf("digest %q is not a sha256", Digest())
	}
}

// **Consent is read before the search recipe.** Order is the whole change: a model that reads the
// search instruction first acts on it, and a privacy sentence at the bottom arrives after the search
// has already run. This test pins the order, not just the presence.
func TestPrivacyComesBeforeSearch(t *testing.T) {
	block := string(Block())
	privacy := strings.Index(block, "涉及个人隐私的查询")
	search := strings.Index(block, "开始任何任务前")
	if privacy < 0 || search < 0 {
		t.Fatalf("the two lines are not both present:\n%s", block)
	}
	if privacy > search {
		t.Error("the privacy line comes after the search instruction")
	}
}

// **The shape of what `prompt print` writes** (v27 acceptance 2): no mark-up, no leading blank line,
// and exactly one newline at the end. A leading blank would push a blank line into the person's file
// wherever they paste it; two trailing newlines would leave a gap below the paragraph.
func TestTheBlockStartsAndEndsCleanly(t *testing.T) {
	block := Block()
	if len(block) == 0 {
		t.Fatal("the block is empty")
	}
	if block[0] == '\n' {
		t.Error("the block starts with a blank line")
	}
	if strings.HasSuffix(string(block), "\n\n") {
		t.Error("the block ends with more than one newline")
	}
}

func TestNormaliseIsWhatMakesACrlfCopyEqual(t *testing.T) {
	crlf := []byte(strings.ReplaceAll(string(Block()), "\n", "\r\n"))
	if !strings.Contains(string(Normalise(crlf)), strings.TrimSuffix(string(Block()), "\n")) {
		t.Error("a CRLF copy did not normalise back to the block")
	}
}

// **What `prompt print` emits is this file, byte for byte** (v27 acceptance 1). The binary embeds
// `block.md`, so the only way the two could drift is a build that skipped the embed — this test is
// what makes 「两个来源逐字节一致」 a checked fact rather than a reading of the source tree.
func TestThePrintedBlockIsTheFileOnDisk(t *testing.T) {
	onDisk, err := os.ReadFile("block.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Normalise(onDisk), Block()) {
		t.Error("the embedded block and block.md have drifted apart")
	}
}
