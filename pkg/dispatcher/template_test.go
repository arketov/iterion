package dispatcher

import (
	"strings"
	"testing"
	"time"

	"github.com/arketov/iterion/pkg/dispatcher/tracker"
)

func TestParseTemplateOK(t *testing.T) {
	cases := []string{
		"plain text",
		"hello {{issue.title}}",
		"{{issue.identifier}}: {{issue.title}}\n\n{{issue.body}}",
		"workspace={{dispatcher.workspace_path}}",
		"sev={{issue.fields.severity}} prio={{issue.priority}}",
		"url={{issue.metadata.html_url}}",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := ParseTemplate(c); err != nil {
				t.Fatalf("ParseTemplate(%q): %v", c, err)
			}
		})
	}
}

func TestParseTemplateRejectsBadRefs(t *testing.T) {
	cases := map[string]string{
		"missing close":      "hello {{issue.title",
		"unknown namespace":  "{{bogus.x}}",
		"unknown issue":      "{{issue.notreal}}",
		"unknown dispatcher": "{{dispatcher.nope}}",
		"no path":            "{{issue}}",
		"fields needs key":   "{{issue.fields}}",
		"metadata needs key": "{{issue.metadata}}",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate(src); err == nil {
				t.Fatalf("expected parse error for %q", src)
			}
		})
	}
}

func TestRenderHappyPath(t *testing.T) {
	tpl, err := ParseTemplate("[{{issue.identifier}}] {{issue.title}} (prio={{issue.priority}}) at {{dispatcher.workspace_path}}")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	ctx := TemplateContext{
		Issue: tracker.Issue{
			Identifier: "iss-42",
			Title:      "Do the thing",
			Priority:   5,
		},
		Dispatcher: DispatcherVars{
			WorkspacePath: "/tmp/ws",
		},
	}
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "[iss-42] Do the thing (prio=5) at /tmp/ws"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderMissingFieldIsEmpty(t *testing.T) {
	tpl, _ := ParseTemplate("sev={{issue.fields.severity}}")
	got, err := tpl.Render(TemplateContext{
		Issue: tracker.Issue{Fields: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "sev=" {
		t.Fatalf("missing field should render empty, got %q", got)
	}
}

func TestRenderLabels(t *testing.T) {
	tpl, _ := ParseTemplate("{{issue.labels}} | {{issue.labels_list}}")
	got, _ := tpl.Render(TemplateContext{
		Issue: tracker.Issue{Labels: []string{"a", "b"}},
	})
	if !strings.Contains(got, "a,b") {
		t.Fatalf("labels joined wrong: %q", got)
	}
	if !strings.Contains(got, "[a,b]") {
		t.Fatalf("labels_list wrong: %q", got)
	}
}

func TestRenderDates(t *testing.T) {
	tpl, _ := ParseTemplate("{{issue.created_at}}")
	when, _ := time.Parse(time.RFC3339, "2026-05-15T10:00:00Z")
	got, err := tpl.Render(TemplateContext{Issue: tracker.Issue{CreatedAt: when}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "2026-05-15T10:00:00Z" {
		t.Fatalf("date format wrong: %q", got)
	}
}

func TestRenderNilTemplate(t *testing.T) {
	var tpl *Template
	got, err := tpl.Render(TemplateContext{})
	if err != nil || got != "" {
		t.Fatalf("nil template should render empty: %v %q", err, got)
	}
}
