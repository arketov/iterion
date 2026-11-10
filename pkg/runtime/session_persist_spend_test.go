package runtime

import (
	"math"
	"testing"
)

func sessionTotalOutput(session string, totalUSD float64) map[string]any {
	return map[string]any{
		"_cost_usd":              totalUSD,
		"_cost_is_session_total": true,
		"_backend":               "claude_code",
		"_session_id":            session,
	}
}

// A loop revisiting a `session: persist` node reports the session's running
// total each pass. The run must be charged the increment over the node's
// previous committed pass, not the whole total again: 3.876 / 8.291 / 10.391
// is 10.391 of real spend, not 22.558.
func TestUnbookedNodeSpend_PersistSessionTotalBooksIncrementAcrossLoopPasses(t *testing.T) {
	rs := &runState{outputs: map[string]map[string]any{}}
	var booked float64
	for _, total := range []float64{3.876, 8.291, 10.391} {
		out := sessionTotalOutput("s1", total)
		booked += unbookedNodeSpend(rs, "agent", out).costUSD
		rs.outputs["agent"] = out // execLoopAfterExec commits after booking
	}
	if math.Abs(booked-10.391) > 1e-9 {
		t.Fatalf("booked %.3f USD across three passes, want 10.391", booked)
	}
}

// A different session (a fresh start after a failed resume) shares no history
// with the previous pass: its whole total is new spend.
func TestUnbookedNodeSpend_NewSessionOnRevisitBooksFullTotal(t *testing.T) {
	rs := &runState{outputs: map[string]map[string]any{"agent": sessionTotalOutput("s1", 3.0)}}
	got := unbookedNodeSpend(rs, "agent", sessionTotalOutput("s2", 2.0)).costUSD
	if math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("booked %.3f USD for a new session, want 2.000", got)
	}
}
