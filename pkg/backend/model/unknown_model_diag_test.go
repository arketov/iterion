package model

import (
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
)

// liveOnlyStub is a live table that knows exactly one model the embedded
// snapshot does not.
func liveOnlyStub(provider, id string) bool {
	return provider+"/"+id == "anthropic/claude-live-only"
}

func unknownModelWorkflow() *ir.Workflow {
	return &ir.Workflow{Nodes: map[string]ir.Node{
		"a": &ir.AgentNode{
			BaseNode:  ir.BaseNode{ID: "a"},
			LLMFields: ir.LLMFields{Model: "anthropic/claude-sonnet-9-9"},
			Fallbacks: []ir.Fallback{
				{Name: "f1", Model: "openai/gpt-9-9"},
				{Name: "f2", Model: ""},
				{Name: "f3", Model: "openai/gpt-0-0", Action: ir.FallbackActionSkip},
			},
		},
		"j": &ir.JudgeNode{
			BaseNode:  ir.BaseNode{ID: "j"},
			LLMFields: ir.LLMFields{Model: "anthropic/claude-sonnet-5-5"},
		},
		"r": &ir.RouterNode{
			BaseNode:   ir.BaseNode{ID: "r"},
			RouterMode: ir.RouterLLM,
			LLMFields:  ir.LLMFields{Model: "anthropic/claude-haiku-9-9"},
		},
		"cond": &ir.RouterNode{
			BaseNode:   ir.BaseNode{ID: "cond"},
			RouterMode: ir.RouterCondition,
			LLMFields:  ir.LLMFields{Model: "anthropic/ignored-typo"},
		},
	}}
}

func TestUnknownModelDiagnostics(t *testing.T) {
	got := unknownModelDiagnostics(unknownModelWorkflow(), liveOnlyStub)

	var ids []string
	for _, d := range got {
		ids = append(ids, d.NodeID)
		if d.Code != ir.DiagUnknownModel || d.Severity != ir.SeverityWarning || d.Hint == "" {
			t.Errorf("bad diagnostic: %+v", d)
		}
	}
	if want := "a,a,r"; strings.Join(ids, ",") != want {
		t.Fatalf("nodes = %v, want %s (primary and fallback of a, the LLM router; judge, condition router and skip route silent): %+v", ids, want, got)
	}
	if !strings.Contains(got[0].Message, "anthropic/claude-sonnet-9-9") {
		t.Errorf("message does not name the model: %s", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "as of 20") {
		t.Errorf("message does not carry the snapshot date: %s", got[0].Message)
	}
	if !strings.Contains(got[1].Message, "openai/gpt-9-9") || !strings.Contains(got[1].Message, "fallback") {
		t.Errorf("fallback message: %s", got[1].Message)
	}
}

func TestUnknownModelDiagnostics_SpecsThatStaySilent(t *testing.T) {
	for _, spec := range []string{
		"anthropic/claude-sonnet-5-5",
		"anthropic/claude-opus-5-5[1m]",
		"openai/gpt-5.5",
		"anthropic/glm-5.2",
		"${MODEL:-anthropic/typo}",
		"{{vars.m}}",
		"claude-opus-4-7",
		"openai_compatible/anything",
		"xai/grok-9",
		"bedrock/x",
		"moonshot/x",
		"anthropic/claude-live-only",
		"",
	} {
		wf := &ir.Workflow{Nodes: map[string]ir.Node{
			"a": &ir.AgentNode{BaseNode: ir.BaseNode{ID: "a"}, LLMFields: ir.LLMFields{Model: spec}},
		}}
		if got := unknownModelDiagnostics(wf, liveOnlyStub); len(got) != 0 {
			t.Errorf("spec %q warned: %+v", spec, got)
		}
	}
}

func TestUnknownModelDiagnostics_NilWorkflow(t *testing.T) {
	if got := unknownModelDiagnostics(nil, liveOnlyStub); got != nil {
		t.Errorf("nil workflow gave %+v", got)
	}
}
