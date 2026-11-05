package gitlab

import (
	"strconv"

	"github.com/arketov/iterion/pkg/forge"
)

// gitlabHook is the project-hook shape GitLab returns and accepts. GitLab
// models event subscriptions as BOOLEAN fields, not an events array — the
// reason this client exists separately from the github/forgejo ones.
type gitlabHook struct {
	ID                  int64  `json:"id"`
	URL                 string `json:"url"`
	MergeRequestsEvents bool   `json:"merge_requests_events"`
	NoteEvents          bool   `json:"note_events"`
	IssuesEvents        bool   `json:"issues_events"`
	PushEvents          bool   `json:"push_events"`
}

// toHandle converts GitLab's boolean event flags back to the native event
// names the orchestrator reasons about ("merge_request" / "note").
func (h gitlabHook) toHandle() forge.HookHandle {
	var events []string
	if h.MergeRequestsEvents {
		events = append(events, "merge_request")
	}
	if h.NoteEvents {
		events = append(events, "note")
	}
	if h.IssuesEvents {
		events = append(events, "issues")
	}
	return forge.HookHandle{
		ID:     strconv.FormatInt(h.ID, 10),
		URL:    h.URL,
		Events: events,
		Active: true,
	}
}

// hookBody builds the GitLab POST/PUT /hooks request body, translating the
// native event names in spec.Events to the boolean fields GitLab expects.
// The secret token is only included when present, so an event-only update
// (empty Secret) leaves the existing token untouched.
func hookBody(spec forge.HookSpec) map[string]any {
	b := map[string]any{
		"url":                     spec.URL,
		"enable_ssl_verification": true,
		"push_events":             false,
		"merge_requests_events":   hasEvent(spec.Events, "merge_request"),
		"note_events":             hasEvent(spec.Events, "note"),
		"issues_events":           hasEvent(spec.Events, "issues"),
	}
	if spec.Secret != "" {
		b["token"] = spec.Secret
	}
	return b
}

func hasEvent(events []string, name string) bool {
	for _, e := range events {
		if e == name {
			return true
		}
	}
	return false
}
