package api

import "testing"

// v76 (ADR 0215): the two ceiling sentences are voice-spec §数量上限的两句话, filled with the counts
// the refusal carried. The numbers are the server's — a constant here would go stale the day the
// limits become per-tier.
func TestSkillQuotaSentence(t *testing.T) {
	cases := []struct {
		name   string
		detail IssueDetail
		want   string
	}{
		{
			name:   "namespace",
			detail: IssueDetail{Field: "quota", Issue: "namespace", ActualCount: 200, LimitCount: 200},
			want: "这个命名空间已有 200 个 skill，达到上限 200 个。" +
				"删除不再使用的 skill 后可继续创建。",
		},
		{
			name:   "library",
			detail: IssueDetail{Field: "quota", Issue: "library", ActualCount: 500, LimitCount: 500},
			want:   "共享给我和我加入的 skill 已有 500 个，达到上限 500 个。移出库后可继续入库。",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			refusal := &ServerError{
				Status:     409,
				Code:       "skill_quota_exceeded",
				Message:    "the server's own English sentence",
				DetailList: []IssueDetail{test.detail},
			}
			if got := refusal.Error(); got != test.want {
				t.Errorf("sentence = %q, want %q", got, test.want)
			}
		})
	}
}

// A refusal without the counts is an older server: the server's own message, which is what the
// console shows for the same input. Inventing a sentence here would make the two disagree.
func TestSkillQuotaSentenceWithoutCounts(t *testing.T) {
	refusal := &ServerError{Status: 409, Code: "skill_quota_exceeded", Message: "full"}
	if got := refusal.Error(); got != "full" {
		t.Errorf("sentence = %q, want the server message", got)
	}
}
