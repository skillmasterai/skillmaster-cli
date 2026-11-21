package main

import (
	"net/http"
	"strings"
	"testing"
)

// v89: the sentence printed before `--yes` names what actually happens. A shared private entry
// ends the grant and loses access; a shared public one keeps the public rung; a joined one only
// drops the reference; a created one cannot leave; a read that cannot answer covers both. The
// command reads the relation — a GET is not an execution — and never sends the write.
//
// **Only durable facts**: whether the skill can be called or rejoined right now is not promised,
// because an offline skill or one inside its ramp refuses joins and answers 503.
func TestLeaveWithoutYesNamesTheRealConsequence(t *testing.T) {
	library := func(source string, publicRung string) string {
		rung := "null"
		if publicRung != "" {
			rung = `"` + publicRung + `"`
		}
		return `{"library":{"source":` + source + `,"active":true,"joinable":false,` +
			`"can_remove":true,"public_rung":` + rung + `}}`
	}
	for _, test := range []struct {
		name    string
		status  int
		body    string
		want    string
		keeping bool
	}{
		{name: "我加入的", status: http.StatusOK, body: library(`"joined"`, ""),
			want: "它仍在公开库", keeping: true},
		{name: "共享给我的 · 私有", status: http.StatusOK, body: library(`"shared"`, ""),
			want: "你会失去对它的访问权", keeping: true},
		{name: "私有共享说清回来的路", status: http.StatusOK, body: library(`"shared"`, ""),
			want: "需要分享方重新邀请你", keeping: true},
		{name: "共享给我的 · 公开", status: http.StatusOK, body: library(`"shared"`, "browse"),
			want: "它仍是公开的", keeping: true},
		{name: "休眠的共享条目", status: http.StatusOK,
			body: `{"library":{"source":"shared","active":false,"joinable":false,` +
				`"can_remove":true,"public_rung":"browse"}}`,
			want: "现在不生效", keeping: true},
		{name: "我创建的", status: http.StatusOK, body: library(`"created"`, ""),
			want: "你创建的，不能移出库"},
		{name: "不在库", status: http.StatusOK, body: library(`null`, ""),
			want: "不在你的库里"},
		{name: "读不到", status: http.StatusNotFound,
			body: `{"error":{"code":"skill_not_found","message":"refused"}}`,
			want: "如果它是别人共享给你的", keeping: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeServer(t, func(request recordedRequest) (int, string) {
				if request.Method == http.MethodGet && strings.HasSuffix(request.Path, "/tags") {
					return test.status, test.body
				}
				return http.StatusNoContent, ""
			})
			stdout, _, status := runCaptured(t, "skill", "leave", "dev/pdf-tools")
			if status == 0 {
				t.Fatal("a write without --yes succeeded")
			}
			if !strings.Contains(stdout, test.want) {
				t.Errorf("the consequence is missing %q:\n%s", test.want, stdout)
			}
			if test.keeping && !strings.Contains(stdout, "我的标签与我的适用场景会保留") {
				t.Errorf("the sentence does not say the tags stay:\n%s", stdout)
			}
			if !strings.Contains(stdout, "skmr skill leave dev/pdf-tools --yes") {
				t.Errorf("the full command is missing:\n%s", stdout)
			}
			if len(fake.requests) != 1 || fake.requests[0].Method != http.MethodGet {
				t.Errorf("a refused write did more than read: %+v", fake.requests)
			}
		})
	}
}

// The discovery page's undo is a narrower action: only the join reference goes, so a share that
// arrived afterwards stays. The CLI exposes it as a flag on the same command (ADR 0081).
func TestLeaveJoinOnlySendsTheNarrowUndo(t *testing.T) {
	fake := newFakeServer(t, func(request recordedRequest) (int, string) {
		return http.StatusOK, `{"changed":true}`
	})
	if out := mustRun(t, "skill", "leave", "dev/pdf-tools", "--yes", "--join-only"); out !=
		"已撤销你在 dev/pdf-tools 上的加入引用。\n" {
		t.Errorf("output = %q", out)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("requests = %+v", fake.requests)
	}
	got := fake.requests[0]
	if got.Method != http.MethodDelete || got.Path != "/api/v1/skills/dev/pdf-tools/library" ||
		got.Query.Get("reference") != "join" {
		t.Errorf("the narrow undo did not travel: %+v", got)
	}
}

// **The narrow undo never ends a share**, so its preview must not borrow the exit's sentence — not
// even for a shared entry, whose displayed source is "shared".
func TestLeaveJoinOnlyWithoutYesNamesTheNarrowUndo(t *testing.T) {
	fake := newFakeServer(t, func(request recordedRequest) (int, string) {
		if request.Method == http.MethodGet {
			return http.StatusOK, `{"library":{"source":"shared","active":true,
				"joinable":false,"can_remove":true,"public_rung":null}}`
		}
		return http.StatusNoContent, ""
	})
	stdout, _, status := runCaptured(t, "skill", "leave", "dev/pdf-tools", "--join-only")
	if status == 0 {
		t.Fatal("a write without --yes succeeded")
	}
	if strings.Contains(stdout, "结束这段共享") || strings.Contains(stdout, "失去对它的访问权") {
		t.Errorf("the narrow undo borrowed the exit's consequence:\n%s", stdout)
	}
	if !strings.Contains(stdout, "不结束任何共享") {
		t.Errorf("the narrow consequence is missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "skmr skill leave dev/pdf-tools --yes --join-only") {
		t.Errorf("the full command is missing:\n%s", stdout)
	}
	if len(fake.requests) != 0 {
		t.Errorf("the narrow preview read or wrote: %+v", fake.requests)
	}
}

// `join` has no such half: refusing the flag keeps one command's two verbs from sharing a meaning
// that only one of them has.
func TestJoinRefusesJoinOnly(t *testing.T) {
	newFakeServer(t, func(recordedRequest) (int, string) { return http.StatusNoContent, "" })
	if _, _, status := runCaptured(t, "skill", "join", "dev/pdf-tools", "--yes",
		"--join-only"); status == 0 {
		t.Error("join accepted --join-only")
	}
}
