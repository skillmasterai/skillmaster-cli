package api

import (
	"context"
	"encoding/json"
)

// RuleReport is the account-level "is the rule installed?" answer (v94, ADR 0242).
//
// **Seven fields, all booleans or enums.** No path, file name, prompt text or device identifier: the
// coverage question is answered without any of them, and each one added would be a larger privacy
// surface for the same number.
type RuleReport struct {
	Agent              string `json:"agent"`
	DefaultRulePresent bool   `json:"default_rule_present"`
	// A pointer so that "no install record" is left out rather than sent as false: absent means
	// unknown, and the server stores it as NULL.
	AgentRulePresent *bool  `json:"agent_rule_present,omitempty"`
	RuleVersion      int    `json:"rule_version"`
	Declined         bool   `json:"declined"`
	CLIVersion       string `json:"cli_version"`
	OS               string `json:"os"`
}

// ReportRuleInstall sends one report. The route answers 204 and has nothing to read.
func (c Client) ReportRuleInstall(ctx context.Context, report RuleReport) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = c.postJSON(ctx, APIPrefix+"/cli/rule-reports", payload)
	return err
}
