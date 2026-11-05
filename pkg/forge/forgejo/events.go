package forgejo

import (
	"strconv"

	"github.com/arketov/iterion/pkg/forge"
)

// forgejoHook is the repo-hook shape Forgejo/Gitea returns. Like GitHub it
// uses an `events` array + a `config` map (string-valued on Gitea).
type forgejoHook struct {
	ID     int64    `json:"id"`
	Type   string   `json:"type"`
	Events []string `json:"events"`
	Active bool     `json:"active"`
	Config struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"config"`
}

func (h forgejoHook) toHandle() forge.HookHandle {
	return forge.HookHandle{
		ID:     strconv.FormatInt(h.ID, 10),
		URL:    h.Config.URL,
		Events: h.Events,
		Active: h.Active,
	}
}

// configMap is Gitea's string-valued hook config. The secret signs the body
// (Forgejo is HMAC mode, the webhook default).
func configMap(spec forge.HookSpec) map[string]string {
	m := map[string]string{"url": spec.URL, "content_type": "json"}
	if spec.Secret != "" {
		m["secret"] = spec.Secret
	}
	return m
}

// hookBody is the CreateHookOption body. type "gitea" is accepted by both
// Gitea and Forgejo (Forgejo keeps the gitea webhook type for compat).
func hookBody(spec forge.HookSpec) map[string]any {
	return map[string]any{
		"type":   "gitea",
		"active": spec.Active,
		"events": spec.Events,
		"config": configMap(spec),
	}
}

// editBody is the EditHookOption body (no type on edit).
func editBody(spec forge.HookSpec) map[string]any {
	return map[string]any{
		"active": spec.Active,
		"events": spec.Events,
		"config": configMap(spec),
	}
}
