package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/arketov/iterion/pkg/dsl/ir"
)

func freeNodesWorkflow() *ir.Workflow {
	return &ir.Workflow{
		Name:  "free_nodes_test",
		Entry: "agent",
		Nodes: map[string]ir.Node{
			"agent":   &ir.AgentNode{BaseNode: ir.BaseNode{ID: "agent"}},
			"tool":    &ir.ToolNode{BaseNode: ir.BaseNode{ID: "tool"}},
			"compute": &ir.ComputeNode{BaseNode: ir.BaseNode{ID: "compute"}},
			"done":    &ir.DoneNode{BaseNode: ir.BaseNode{ID: "done"}},
			"fail":    &ir.FailNode{BaseNode: ir.BaseNode{ID: "fail"}},
		},
		Schemas: map[string]*ir.Schema{},
		Prompts: map[string]*ir.Prompt{},
		Vars:    map[string]*ir.Var{},
		Loops:   map[string]*ir.Loop{},
		Budget:  &ir.Budget{MaxCostUSD: 20},
	}
}

func freeNodesState(t *testing.T, e *Engine, spent float64, branchLocal bool) *runState {
	t.Helper()
	b := newSharedBudget(e.workflow.Budget, nil)
	b.costUsed = spent
	return &runState{ctx: context.Background(), runID: "run-free-nodes", budget: b, branchLocal: branchLocal}
}

// Tool, compute and terminal nodes start past the 90% line and past the cap;
// agent nodes stay gated.
func TestBudgetGateLetsFreeNodesThrough(t *testing.T) {
	t.Setenv("ITERION_BUDGET_EXIT_GRACE", "off")
	for _, spent := range []float64{19, 21} { // 95% and 105% of 20
		for _, id := range []string{"tool", "compute", "done", "fail"} {
			wf := freeNodesWorkflow()
			e := New(wf, tmpStore(t), newStubExecutor())
			rs := freeNodesState(t, e, spent, false)
			if err := e.checkBudgetBeforeExec(rs, id); err != nil {
				t.Errorf("%s at $%.0f/$20 refused: %v", id, spent, err)
			}
		}
	}
}

func TestBudgetGateStillRefusesAgentNode(t *testing.T) {
	t.Setenv("ITERION_BUDGET_EXIT_GRACE", "off")
	for _, spent := range []float64{19, 21} {
		wf := freeNodesWorkflow()
		e := New(wf, tmpStore(t), newStubExecutor())
		rs := freeNodesState(t, e, spent, false)
		err := e.checkBudgetBeforeExec(rs, "agent")
		if err == nil || !strings.Contains(err.Error(), "budget") {
			t.Errorf("agent at $%.0f/$20: want a budget refusal, got %v", spent, err)
		}
	}
}

func TestBudgetGateFreeNodeInBranchKeepsOldBehaviour(t *testing.T) {
	t.Setenv("ITERION_BUDGET_EXIT_GRACE", "off")
	wf := freeNodesWorkflow()
	e := New(wf, tmpStore(t), newStubExecutor())
	rs := freeNodesState(t, e, 19, true)
	err := e.checkBudgetBeforeExec(rs, "tool")
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Errorf("tool in a branch at 95%%: want a budget refusal, got %v", err)
	}
}
