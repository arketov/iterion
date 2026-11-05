package delegate

import (
	"testing"

	"github.com/arketov/iterion/pkg/backend/delegate/claudesdk"
)

// TestApplyClaudeCodeSessionMeta exercises the merge between the session-
// streamed metadata (model name, peak context load) and the final
// ResultMessage's per-model usage payload. The function is the single
// site that decides what the run-view will see for "effective model"
// and "context window" on a claude_code node, so the cases below cover
// the realistic combinations we observe in practice.
func TestApplyClaudeCodeSessionMeta(t *testing.T) {
	tests := []struct {
		name              string
		meta              sessionMeta
		rm                *claudesdk.ResultMessage
		wantModel         string
		wantContextWindow int
		wantMaxOutput     int
		wantPeak          int
	}{
		{
			name: "matching ModelUsage entry — happy path",
			meta: sessionMeta{
				effectiveModel:  "claude-opus-4-7",
				peakContextLoad: 42_000,
			},
			rm: &claudesdk.ResultMessage{
				ModelUsage: map[string]claudesdk.ModelUsage{
					"claude-opus-4-7": {
						InputTokens:     12345,
						OutputTokens:    678,
						ContextWindow:   1_000_000,
						MaxOutputTokens: 64_000,
					},
				},
			},
			wantModel:         "claude-opus-4-7",
			wantContextWindow: 1_000_000,
			wantMaxOutput:     64_000,
			wantPeak:          42_000,
		},
		{
			name: "third-party model name (GLM via z.ai proxy)",
			meta: sessionMeta{
				effectiveModel:  "glm-4.6",
				peakContextLoad: 90_000,
			},
			rm: &claudesdk.ResultMessage{
				ModelUsage: map[string]claudesdk.ModelUsage{
					"glm-4.6": {
						ContextWindow:   200_000,
						MaxOutputTokens: 8_192,
					},
				},
			},
			wantModel:         "glm-4.6",
			wantContextWindow: 200_000,
			wantMaxOutput:     8_192,
			wantPeak:          90_000,
		},
		{
			name: "missing system/init — single-entry ModelUsage fallback",
			meta: sessionMeta{peakContextLoad: 5_000},
			rm: &claudesdk.ResultMessage{
				ModelUsage: map[string]claudesdk.ModelUsage{
					"claude-sonnet-4-6": {
						ContextWindow:   200_000,
						MaxOutputTokens: 8_192,
					},
				},
			},
			wantModel:         "claude-sonnet-4-6",
			wantContextWindow: 200_000,
			wantMaxOutput:     8_192,
			wantPeak:          5_000,
		},
		{
			name: "proxy didn't fill ModelUsage — model preserved, window unknown",
			meta: sessionMeta{
				effectiveModel:  "kimi-k2",
				peakContextLoad: 18_000,
			},
			rm:                &claudesdk.ResultMessage{ModelUsage: nil},
			wantModel:         "kimi-k2",
			wantContextWindow: 0,
			wantMaxOutput:     0,
			wantPeak:          18_000,
		},
		{
			name: "multiple ModelUsage entries + unknown effective model — ambiguous, no fallback",
			meta: sessionMeta{peakContextLoad: 1_234},
			rm: &claudesdk.ResultMessage{
				ModelUsage: map[string]claudesdk.ModelUsage{
					"claude-sonnet-4-6": {ContextWindow: 200_000},
					"claude-haiku-4-5":  {ContextWindow: 200_000},
				},
			},
			wantModel:         "",
			wantContextWindow: 0,
			wantMaxOutput:     0,
			wantPeak:          1_234,
		},
		{
			name: "effective model declared but ModelUsage keyed differently — no match, no silent fallback when ambiguous",
			meta: sessionMeta{
				effectiveModel:  "deepseek-v3",
				peakContextLoad: 7_000,
			},
			rm: &claudesdk.ResultMessage{
				ModelUsage: map[string]claudesdk.ModelUsage{
					"some-other-name": {ContextWindow: 128_000},
					"yet-another":     {ContextWindow: 64_000},
				},
			},
			wantModel:         "deepseek-v3",
			wantContextWindow: 0,
			wantMaxOutput:     0,
			wantPeak:          7_000,
		},
		{
			name: "nil ResultMessage — only session meta survives",
			meta: sessionMeta{
				effectiveModel:  "claude-opus-4-7",
				peakContextLoad: 100,
			},
			rm:                nil,
			wantModel:         "claude-opus-4-7",
			wantContextWindow: 0,
			wantMaxOutput:     0,
			wantPeak:          100,
		},
		{
			name:              "everything empty — no panic, all zero",
			meta:              sessionMeta{},
			rm:                &claudesdk.ResultMessage{},
			wantModel:         "",
			wantContextWindow: 0,
			wantMaxOutput:     0,
			wantPeak:          0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out Result
			applyClaudeCodeSessionMeta(&out, tc.rm, tc.meta)
			if out.EffectiveModel != tc.wantModel {
				t.Errorf("EffectiveModel: got %q, want %q", out.EffectiveModel, tc.wantModel)
			}
			if out.ContextWindow != tc.wantContextWindow {
				t.Errorf("ContextWindow: got %d, want %d", out.ContextWindow, tc.wantContextWindow)
			}
			if out.MaxOutputTokens != tc.wantMaxOutput {
				t.Errorf("MaxOutputTokens: got %d, want %d", out.MaxOutputTokens, tc.wantMaxOutput)
			}
			if out.PeakInputTokens != tc.wantPeak {
				t.Errorf("PeakInputTokens: got %d, want %d", out.PeakInputTokens, tc.wantPeak)
			}
		})
	}
}

// TestApplyClaudeCodeSessionMeta_NilTarget guards the nil-Result early
// return path. The caller never passes nil today, but the helper is
// defensive so a refactor doesn't risk a nil-deref crash on a corner
// case like the ask_user pause path returning early.
func TestApplyClaudeCodeSessionMeta_NilTarget(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil target should not panic, got: %v", r)
		}
	}()
	applyClaudeCodeSessionMeta(nil, &claudesdk.ResultMessage{}, sessionMeta{effectiveModel: "x"})
}
