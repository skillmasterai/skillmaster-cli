package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// v76 (ADR 0214): the ceiling sentence reaches the command's own output — what a person sees — and
// the status is non-zero. The unit tests prove the formatter; this proves the wiring, which is
// where the sentences were once lost (`skill submit` built its own error and dropped the counts).
func TestQuotaRefusalsReachTheCommandOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		argv   []string
		issue  string
		actual int
		limit  int
		want   string
	}{
		{
			name:  "library",
			argv:  []string{"skill", "join", "dev/pdf-tools", "--yes"},
			issue: "library", actual: 500, limit: 500,
			want: "共享给我和我加入的 skill 已有 500 个，达到上限 500 个。移出库后可继续入库。",
		},
		{
			name:  "namespace",
			argv:  []string{"skill", "create", "one-too-many", "--description", "d"},
			issue: "namespace", actual: 200, limit: 200,
			want: "这个命名空间已有 200 个 skill，达到上限 200 个。删除不再使用的 skill 后可继续创建。",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			newFakeServer(t, func(recordedRequest) (int, string) {
				return http.StatusConflict, fmt.Sprintf(
					`{"error":{"code":"skill_quota_exceeded","message":"full","details":[`+
						`{"field":"quota","issue":%q,"actual_count":%d,"limit_count":%d}]}}`,
					test.issue, test.actual, test.limit)
			})

			_, stderr, status := runCaptured(t, test.argv...)
			if status != 1 {
				t.Fatalf("%v: status %d, want 1 (stderr %q)", test.argv, status, stderr)
			}
			if !strings.Contains(stderr, test.want) {
				t.Errorf("%v: stderr %q, want %q", test.argv, stderr, test.want)
			}
		})
	}
}
