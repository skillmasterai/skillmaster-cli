// `settings`: the account's behaviour preferences (v87).

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
)

// settingsVerb reads one preference, or writes it with a flag.
//
// **A flag rather than `settings set`.** Every other command in this CLI is a verb plus flags, and a
// two-word subcommand would be the only place a noun grew a child. Reading is the bare command;
// `--public-recall on|off` is the write. The console's switch and this write the same account field.
func settingsVerb(ctx context.Context, args []string) error {
	// **Decided before the loop**: a usage error must be the JSON object even when `--json` comes
	// after the bad value, so the parse order must not decide the shape.
	jsonMode := jsonModeIn(args, "--public-recall")
	var set *bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--public-recall":
			raw, err := flagValue(args, i, "--public-recall", "settings")
			if err != nil {
				return versionErr(jsonMode, err, "")
			}
			value := strings.ToLower(raw)
			if value != "on" && value != "off" {
				return versionErr(jsonMode, usagef("--public-recall 只接受 on 或 off，收到 %q。%s",
					raw, usageAt("settings")), "")
			}
			on := value == "on"
			set = &on
			i++
		case "--json":
			// jsonMode was already set from the whole argument list.
		default:
			return versionErr(jsonMode, usagef("不认识的参数 %q。%s", args[i],
				usageAt("settings")), "")
		}
	}

	found, err := authenticatedWith(ctx, false, func(client api.Client) (api.Settings, error) {
		if set != nil {
			return client.SetPublicRecallHint(ctx, *set)
		}
		return client.AccountSettings(ctx)
	})
	if err != nil {
		return versionErr(jsonMode, err, "")
	}

	if jsonMode {
		out, marshalErr := json.Marshal(struct {
			PublicRecallHint bool `json:"public_recall_hint"`
		}{found.PublicRecallHint})
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Println(string(out))
		return nil
	}
	fmt.Printf("库内结果不足时列出公开库的匹配：%s\n", enabledWord(found.PublicRecallHint))
	return nil
}

func enabledWord(on bool) string {
	if on {
		return "开启"
	}
	return "关闭"
}
