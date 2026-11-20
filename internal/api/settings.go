package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Settings is the account's behaviour preferences (v87). One field today.
//
// The wire is snake_case; a struct that names only the fields it needs is deliberate, so a future
// preference is not a change here.
type Settings struct {
	PublicRecallHint bool `json:"public_recall_hint"`
}

// AccountSettings reads them.
func (c Client) AccountSettings(ctx context.Context) (Settings, error) {
	body, err := c.get(ctx, APIPrefix+"/me/settings")
	if err != nil {
		return Settings{}, err
	}
	return decodeSettings(body)
}

// SetPublicRecallHint writes the one preference and returns the stored answer.
func (c Client) SetPublicRecallHint(ctx context.Context, on bool) (Settings, error) {
	body, err := c.sendJSON(ctx, http.MethodPut, APIPrefix+"/me/settings",
		map[string]any{"public_recall_hint": on})
	if err != nil {
		return Settings{}, err
	}
	return decodeSettings(body)
}

func decodeSettings(body []byte) (Settings, error) {
	var settings Settings
	if err := json.Unmarshal(body, &settings); err != nil {
		return Settings{}, fmt.Errorf("读取 %s/me/settings 的响应：%w", APIPrefix, err)
	}
	return settings, nil
}
