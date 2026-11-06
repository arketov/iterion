# HTTP failures and partial identity reads

`internal/httpx.WriteError` and `WriteErrorJSON` own public error responses.
A 5xx carries `error: "internal server error"`, a server-generated `request_id`
(also `X-Request-ID`), and `trace_id` when the request has a trace. The cause
goes through `pkg/log`, whose existing `pkg/errtrack` hook reports it. Stable
`error_code` and boolean `retryable` survive; nested diagnostics stay in logs.
4xx refusals keep their actionable payload. Readiness probes use `WriteJSON`:
their operational states are intentional public data, not driver errors.

A 4xx message never names the host's directories either. `WriteErrorJSON` runs
the "error" text of every 4xx through the redactor the server installs
(`httpx.SetErrorRedactor`, wired by `pkg/server/redact_paths.go`;
`internal/pathredact.Host`): a path under a registered root — the workspace and
store the server and a project switch register, plus the home, temp and working
directories — becomes its remainder relative to that root, and any Windows
drive or UNC path and any path under a host-only top-level directory (`/home`,
`/var`, `/tmp`, …) shrinks to its last element. An API route in a message
(`/api/v1/runs/…`) is left alone. Without an installed redactor the text goes
out as it was, so a process that is not the studio server (the dispatcher run
alone) changes nothing. A handler that writes an error with `http.Error` or a
raw map bypasses that boundary and calls `httpx.RedactError` on the text
itself. The bot catalog's discovery diagnostics use the stricter
`pathredact.Strict` — every absolute path, in either spelling on every OS, goes
— because their paths are all the server's.

The server's plain, origin-aware, coded and structured helpers, dispatcher
errors, and plugin failures share that boundary. A response buffer replacing
the network writer must preserve its request/logger binding; webhook workers
bind their own buffer. Keep flush and hijack working through every wrapper.
Correlation IDs must appear in human-readable logs as well as structured
fields: `Logger.WithFields` alone does not print them in human format.

Identity bulk reads decode each Mongo document separately. A completed cursor
returns readable rows plus `*identity.BulkDecodeError`, naming each corrupt
document. Query/cursor errors return no partial result. `/api/auth/me` accepts
only this specific partial result for organizations: a corrupt member org is
`unavailable: true`, with its known membership ID, no asserted org role/name,
and `teams: []`. Missing documents are still absent; a general outage still
fails the whole request. Other bulk consumers may refuse partial reads.

Studio and CLI retain that distinction: unavailable is neither an empty org
nor a restricted account. Org selection is disabled with a visible retry;
healthy siblings remain usable. The credential's team role still selects its
shell, including `config_editor`. CLI JSON keeps the marker, an unavailable org
cannot become the stored default, and slug lookup refuses an incomplete tree
because an unreadable org could hold a duplicate slug; explicit team UUIDs
remain usable and are authorized by the server.

An org memory-quota update answers 5xx if listing its teams or updating any
enforced counter fails. The org row may already be saved; retrying the same
request propagates its desired quota again. A 200 means propagation completed.

Witnesses: `pkg/server/error_chokepoint_test.go`,
`pkg/server/error_path_redaction_test.go`, `internal/pathredact/pathredact_test.go`,
`internal/httpx/redact_test.go`,
`pkg/identity/bulk_decode_test.go` and the real-Mongo bulk suite,
`pkg/cli/remote_identity_partial_test.go`, plus Studio's partial identity and
organization switcher tests. Mutate production code, observe each witness fail
on its assertion, restore identical bytes, then require green.
