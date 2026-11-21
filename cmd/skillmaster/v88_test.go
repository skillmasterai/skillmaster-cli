package main

import (
	"net/http"
	"strings"
	"testing"
)

// v88: the thin answer carries the public skills themselves, as a block of their own after the
// library rows. The sentence that introduces the block is the only thing that says where they come
// from — the rows carry no marker, because an address line has to stay copyable whole.
func TestSearchListsThePublicCandidates(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":3,"public_matches_capped":false,"public_candidates":[{"name":"deck","namespace":"bob","description":"做演示文稿。"},{"name":"keynote","namespace":"cid","description":"排幻灯片。"},{"name":"outline","namespace":"dan","description":"先列大纲。"}]}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "公开库还有 3 条") {
		t.Fatalf("first line = %q, want the public count", first)
	}
	if strings.Contains(first, "--scope all") {
		t.Fatalf("first line = %q, must not send the caller to a second search beside the rows", first)
	}

	library := strings.Index(out, "ann/slides")
	separator := strings.Index(out, "（以下 3 条来自公开库，补齐这次搜索。）")
	firstCandidate := strings.Index(out, "bob/deck")
	if library < 0 || separator < 0 || firstCandidate < 0 {
		t.Fatalf("output = %q, want library row, separator and candidates", out)
	}
	if !(library < separator && separator < firstCandidate) {
		t.Fatalf("output = %q, want library → separator → candidates", out)
	}
	for _, address := range []string{"cid/keynote", "dan/outline"} {
		if !strings.Contains(out, address) {
			t.Errorf("output = %q, want %s", out, address)
		}
	}
}

// With nothing in the library there is no block to separate, so the separator sentence joins the
// first line — one sentence that says both why the answer is thin and what follows.
func TestSearchMergesTheFillIntoAnEmptyFirstLine(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[],"next_cursor":null,"note":null,"public_matches":5,"public_matches_capped":false,"public_candidates":[{"name":"deck","namespace":"bob","description":"做演示文稿。"},{"name":"keynote","namespace":"cid","description":"排幻灯片。"}]}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "你的库没有匹配；公开库还有 5 条，以下 2 条来自公开库，补齐这次搜索。") {
		t.Fatalf("first line = %q, want the merged sentence", first)
	}
	if strings.Contains(out, "没有匹配的 skill。") {
		t.Fatalf("output = %q, must not repeat the server's empty sentence", out)
	}
	// The first line carries the separator sentence, so the fill must not print it again.
	if strings.Count(out, "以下 2 条来自公开库，补齐这次搜索") != 1 {
		t.Fatalf("output = %q, want the fill sentence exactly once", out)
	}
	if !strings.Contains(out, "bob/deck") {
		t.Fatalf("output = %q, want the candidates", out)
	}
}

// The separator names the rows that actually arrived, not the page size that was asked for: the
// public set can simply be smaller than `--limit`.
func TestSearchSeparatorCountsTheRowsItGot(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		// One library row, so the block gets its own separator line rather than the merged
		// first line the empty-library case uses.
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":2,"public_matches_capped":false,"public_candidates":[{"name":"deck","namespace":"bob","description":"做演示文稿。"},{"name":"keynote","namespace":"cid","description":"排幻灯片。"}]}`
	})

	out := mustRun(t, "skills", "search", "做PPT", "--limit", "5")
	if !strings.Contains(out, "（以下 2 条来自公开库，补齐这次搜索。）") {
		t.Fatalf("output = %q, want the separator to count the two rows it got", out)
	}
	if strings.Contains(out, "以下 5 条") {
		t.Fatalf("output = %q, must not name the page size", out)
	}
}

// A v87 server only has the count. The CLI must stay on the old path verbatim: the second search
// is then the only way to see the skills, so the advice stays.
func TestSearchFallsBackToTheV87HintWithoutCandidates(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":12,"public_matches_capped":false}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "--scope all") {
		t.Fatalf("first line = %q, want the v87 advice when there are no candidates", first)
	}
	if strings.Contains(out, "补齐这次搜索") {
		t.Fatalf("output = %q, must not introduce a block it did not receive", out)
	}
}

// The account switch off means the server sends none of the three fields.
func TestSearchPrintsNothingWhenThePreferenceIsOff(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	if strings.Contains(out, "公开库") || strings.Contains(out, "补齐这次搜索") {
		t.Fatalf("output = %q, want no hint and no fill", out)
	}
}

// A capped count is a floor; the cards are still the closest few. Both facts keep their wording.
func TestSearchKeepsTheCappedWordingWithCandidates(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":200,"public_matches_capped":true,"public_candidates":[{"name":"a0","namespace":"pub","description":"x"},{"name":"a1","namespace":"pub","description":"x"},{"name":"a2","namespace":"pub","description":"x"},{"name":"a3","namespace":"pub","description":"x"},{"name":"a4","namespace":"pub","description":"x"},{"name":"a5","namespace":"pub","description":"x"},{"name":"a6","namespace":"pub","description":"x"},{"name":"a7","namespace":"pub","description":"x"},{"name":"a8","namespace":"pub","description":"x"},{"name":"a9","namespace":"pub","description":"x"}]}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "公开库还有 200 条以上") {
		t.Fatalf("first line = %q, want the capped reading", first)
	}
	// The cap bounds the count, not the shortlist: ten cards still arrive.
	if !strings.Contains(out, "（以下 10 条来自公开库，补齐这次搜索。）") {
		t.Fatalf("output = %q, want ten candidates under a capped count", out)
	}
}
