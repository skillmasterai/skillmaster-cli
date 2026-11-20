package main

import (
	"net/http"
	"strings"
	"testing"
)

// v87: the public pre-check's line is the first thing printed. The same reason the rephrase hint
// sits at the top: both ways this output is cut keep the head and drop the tail.
func TestSearchPrintsThePublicPrecheckFirst(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":12,"public_matches_capped":false}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "公开库还有 12 条") || !strings.Contains(first, "--scope all") {
		t.Fatalf("first line = %q, want the public hint", first)
	}
}

func TestSearchSaysWhenTheLibraryWasEmpty(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[],"next_cursor":null,"note":null,"public_matches":3,"public_matches_capped":false}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	if !strings.Contains(out, "你的库没有匹配；公开库还有 3 条") {
		t.Fatalf("output = %q, want the empty-library wording", out)
	}
}

func TestSearchDoesNotContradictTheEmptyLibraryHint(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[],"next_cursor":null,"note":"没有足够相关的 skill。","public_matches":4,"public_matches_capped":false}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	if !strings.Contains(out, "你的库没有匹配") {
		t.Fatalf("output = %q, want the empty-library hint", out)
	}
	if strings.Contains(out, "没有足够相关的 skill。") {
		t.Fatalf("output = %q, must not say nothing matched anywhere", out)
	}
}

func TestSearchSaysWhenTheCountWasCapped(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":200,"public_matches_capped":true}`
	})

	out := mustRun(t, "skills", "search", "做PPT")
	if !strings.Contains(out, "公开库还有 200 条以上") {
		t.Fatalf("output = %q, want 「200 条以上」 for a capped count", out)
	}
}

// No field (the pre-check did not run) and a zero (it ran and found nothing) are both silent.
func TestSearchStaysQuietWithoutAPublicMatch(t *testing.T) {
	for _, reply := range []string{
		`{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null}`,
		`{"skills":[{"name":"slides","namespace":"ann","description":"做 PPT。"}],"next_cursor":null,"note":null,"public_matches":0,"public_matches_capped":false}`,
	} {
		newFakeServer(t, func(recordedRequest) (int, string) { return http.StatusOK, reply })
		if out := mustRun(t, "skills", "search", "做PPT"); strings.Contains(out, "公开库还有") {
			t.Errorf("output = %q, want no hint for %s", out, reply)
		}
	}
}

func TestSettingsShowsTheStoredPreference(t *testing.T) {
	fake := newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"public_recall_hint":true}`
	})

	out := mustRun(t, "settings")
	if !strings.Contains(out, "库内结果不足时提示公开库：开启") {
		t.Fatalf("output = %q", out)
	}
	if len(wantsRequests(t, fake, http.MethodGet, "/api/v1/me/settings")) != 1 {
		t.Fatal("settings did not read the account preference")
	}
}

func TestSettingsWritesThePreference(t *testing.T) {
	fake := newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"public_recall_hint":false}`
	})

	out := mustRun(t, "settings", "--public-recall", "off")
	if !strings.Contains(out, "库内结果不足时提示公开库：关闭") {
		t.Fatalf("output = %q", out)
	}
	puts := wantsRequests(t, fake, http.MethodPut, "/api/v1/me/settings")
	if len(puts) != 1 {
		t.Fatalf("PUTs = %d, want 1", len(puts))
	}
	if !strings.Contains(puts[0].Body, `"public_recall_hint":false`) {
		t.Errorf("body = %q", puts[0].Body)
	}
}

// `--json` promises a JSON failure object on every path, the credential and server ones included.
func TestSettingsJSONFailureIsAnObject(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) {
		return http.StatusInternalServerError, `{"code":"internal_error","message":"boom"}`
	})

	_, stderr, status := runCaptured(t, "settings", "--json")
	if status == 0 {
		t.Fatal("settings --json succeeded against a failing server")
	}
	if !strings.Contains(stderr, `"ok":false`) {
		t.Fatalf("stderr = %q, want the JSON failure object", stderr)
	}
}

// The flag's position must not decide the shape: `--json` after the bad value still means JSON.
func TestSettingsJSONOrderDoesNotDecideTheShape(t *testing.T) {
	_, stderr, status := runCaptured(t, "settings", "--public-recall", "maybe", "--json")
	if status != 2 {
		t.Fatalf("status = %d, want 2\nstderr: %s", status, stderr)
	}
	if !strings.Contains(stderr, `"ok":false`) {
		t.Fatalf("stderr = %q, want the JSON failure object", stderr)
	}
}

func TestSettingsRefusesAnotherWord(t *testing.T) {
	stdout, stderr, status := runCaptured(t, "settings", "--public-recall", "maybe")
	if status == 0 {
		t.Fatalf("--public-recall maybe was accepted:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "on 或 off") {
		t.Errorf("stderr = %q, want the usage sentence", stderr)
	}
}
