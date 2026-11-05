package github

import (
	"strconv"

	"github.com/arketov/iterion/pkg/forge"
)

// githubHook is the repo-hook shape GitHub returns/accepts. Unlike GitLab's
// boolean fields, GitHub models event subscriptions as an `events` array and
// nests the delivery target under `config`.
type githubHook struct {
	ID     int64    `json:"id"`
	Events []string `json:"events"`
	Active bool     `json:"active"`
	Config struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"config"`
}

func (h githubHook) toHandle() forge.HookHandle {
	return forge.HookHandle{
		ID:     strconv.FormatInt(h.ID, 10),
		URL:    h.Config.URL,
		Events: h.Events,
		Active: h.Active,
	}
}

// hookBody builds the GitHub POST/PATCH /hooks request body. spec.Events are
// already GitHub-native names (pull_request, issue_comment) resolved by
// forge.event_map. The secret signs the body (HMAC mode); content_type json
// matches iterion's inbound parser.
func hookBody(spec forge.HookSpec) map[string]any {
	config := map[string]any{
		"url":          spec.URL,
		"content_type": "json",
		"insecure_ssl": "0",
	}
	if spec.Secret != "" {
		config["secret"] = spec.Secret
	}
	return map[string]any{
		"name":   "web",
		"active": spec.Active,
		"events": spec.Events,
		"config": config,
	}
}
