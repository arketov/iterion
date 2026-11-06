package delegate

import "encoding/json"

// fakeClaudeScripted is a stand-in `claude` CLI that plays a script: every
// line of FAKE_CLAUDE_SCRIPT is a stream-json line to emit, or a directive —
// `@sleep <seconds>`, `@wait <substring>` (read stdin, logging each line to
// FAKE_CLAUDE_STDIN_LOG, until one contains the substring), `@drain` (log
// stdin until iterion closes it, then exit). It first consumes stdin up to the
// prompt (the initialize control request precedes it when hooks are set).
//
// Pacing rules (#2201): a `@sleep` runs on the fake CLI's clock while the
// lifecycle's timers — the wave's wait budget while work is held, the
// idle-settle window at a reported idle, the auto-turn grace between turns,
// the answer budget once a message of iterion's is out — run on theirs, and
// on a loaded runner the two stretch differently enough to invert the order
// a scenario needs. So:
//   - a sleep whose ordering matters goes INSIDE a turn (after its init): the
//     lifecycle arms no timer while a turn runs — only the wrap-up's finalize
//     bound, once a turn took the wrap-up, still ticks; the take's bound runs
//     from the last turn activity, so a turn in flight silences it — so the
//     pacing cannot race one;
//   - a wave of held work whose budget must NOT fire settles at message
//     speed — no sleep between the close that opened it and the snapshot
//     that ends it (a budget that must fire is `@wait`ed for instead);
//   - "idle a moment before the re-kicked turn" is lnIdle() then lnRunning()
//     back to back: the idle never holds;
//   - a sleep between turns survives only where nothing armed can spoil the
//     scenario: the budget is the pinned 30-minute default, the fire is
//     suppressed (a result on its way), the fire is the point, or the armed
//     budget is pinned far past the sleep.
const fakeClaudeScripted = `#!/bin/sh
log="${FAKE_CLAUDE_STDIN_LOG:-/dev/null}"
if [ -n "$FAKE_CLAUDE_SPAWN_LOG" ]; then
  printf 'ARGV %s\n' "$*" > "$FAKE_CLAUDE_SPAWN_LOG"
  env | grep '^CLAUDE_CODE_' >> "$FAKE_CLAUDE_SPAWN_LOG"
fi
exec 3<&0
while IFS= read -r line <&3; do
  printf '%s\n' "$line" >> "$log"
  case "$line" in *'"type":"user"'*) break ;; esac
done
while IFS= read -r step; do
  case "$step" in
    '@sleep '*) sleep "${step#@sleep }" ;;
    '@wait '*)
      pat="${step#@wait }"
      while IFS= read -r l <&3; do
        printf '%s\n' "$l" >> "$log"
        case "$l" in *"$pat"*) last="$l"; break ;; esac
      done ;;
    '@replay')
      printf '%s\n' "$last" | sed 's/^{/{"isReplay":true,/' ;;
    '@exit '*) exit "${step#@exit }" ;;
    '@drain')
      while IFS= read -r l <&3; do printf '%s\n' "$l" >> "$log"; done
      exit 0 ;;
    *) printf '%s\n' "$step" ;;
  esac
done < "$FAKE_CLAUDE_SCRIPT"
exit 0
`

func jsonLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func lnInit(version string) string {
	return jsonLine(map[string]any{"type": "system", "subtype": "init", "session_id": "s1", "model": "fake-model",
		"claude_code_version": version, "tools": []any{}, "mcp_servers": []any{}})
}

func lnAssistant(msgID string, parent string, content ...map[string]any) string {
	m := map[string]any{"type": "assistant", "session_id": "s1", "message": map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": "fake-model",
		"content": content, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}}
	if parent != "" {
		m["parent_tool_use_id"] = parent
	}
	return jsonLine(m)
}

func cText(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func cToolUse(id, name string, input map[string]any) map[string]any {
	if input == nil {
		input = map[string]any{}
	}
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func lnToolResult(toolUseID, content string, isErr bool, parent string) string {
	m := map[string]any{"type": "user", "session_id": "s1", "message": map[string]any{"role": "user",
		"content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": content, "is_error": isErr}}}}
	if parent != "" {
		m["parent_tool_use_id"] = parent
	}
	return jsonLine(m)
}

type resultSpec struct {
	subtype    string
	text       string
	turns      int
	cost       float64
	in, out    int
	structured any
}

func lnResult(r resultSpec) string {
	if r.subtype == "" {
		r.subtype = "success"
	}
	m := map[string]any{"type": "result", "subtype": r.subtype, "is_error": r.subtype != "success",
		"result": r.text, "num_turns": r.turns, "duration_ms": 10, "duration_api_ms": 5,
		"total_cost_usd": r.cost, "usage": map[string]any{"input_tokens": r.in, "output_tokens": r.out}, "session_id": "s1"}
	if r.structured != nil {
		m["structured_output"] = r.structured
	}
	return jsonLine(m)
}

// lnSnapshotOf is a background_tasks_changed line listing tasks verbatim.
func lnSnapshotOf(tasks ...map[string]any) string {
	list := make([]any, 0, len(tasks))
	for _, t := range tasks {
		list = append(list, t)
	}
	return jsonLine(map[string]any{"type": "system", "subtype": "background_tasks_changed", "tasks": list, "session_id": "s1"})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
