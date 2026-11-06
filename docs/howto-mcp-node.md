# Give a node an MCP server

How a node of a `.bot` gets the tools of an MCP server, with the
bars documentation server as the worked example, and the
five rules that decide whether it works — most of them invisible until a run
quietly goes without the tools.

Status: the `.bot` below compiles and `iterion validate` raises `C337` on a
misspelled server (both run against this tree). The runtime rules are taken from
[backends](backends.md#claude_code), [sandbox](sandbox.md#mcp-servers-under-a-sandbox)
and [permissions](permissions.md); a live run with the real server was not made
for this page.

## The recipe

`bars` is a stdio MCP server — an executable that speaks MCP on its stdin and
stdout (`bars-mcp.exe` on Windows). You may well have it registered for Claude
Code in `~/.claude.json`. That registration does **not** reach a bot node (rule
1). Declare it in the `.bot` instead:

```iter fragment
dsl: 2

vars:
  question: string = ""

mcp_server bars:
  transport: stdio
  command: "C:/Users/you/AppData/Local/bars/bars-mcp.exe"
  args: []

schema answer:
  text: string

prompt ask_system:
  Answer from the project documentation only. Search it with the bars tools.

prompt ask_user:
  {{vars.question}}

agent ask:
  backend: "claude_code"
  model: "anthropic/claude-sonnet-5-5"
  mcp:
    servers: [bars]
  tools: [read_file, grep, mcp.bars.bars_rag_search, mcp.bars.bars_read]
  input: answer
  output: answer
  system: ask_system
  user: ask_user

workflow docs_question:
  entry: ask
  sandbox: none
  ask -> done
```

Four lines do the work:

- `mcp_server bars:` declares the server. `transport` is `stdio`, `http` or
  `sse`; stdio takes `command` and `args`, the others a `url`. Forward slashes
  are accepted in a Windows path.
- `mcp: servers: [bars]` on the node activates it for that node. The same block
  on the `workflow` activates it for every node.
- `tools: [... mcp.bars.bars_rag_search ...]` names the server's tools:
  `mcp.<server>.<tool>`, or `mcp.<server>.*` for all of them. The spelling
  `mcp__<server>__<tool>` is accepted too.
- `sandbox: none` — rule 5.

Check it without running anything: `iterion validate docs.bot`, then
`iterion validate docs.bot --exec` ([how](quickstart.md#check-before-you-run)).

## The five rules

**1. `claude_code` sees only what iterion hands it.** iterion starts the Claude
Code CLI with `--strict-mcp-config`, so the servers in your `~/.claude.json` do
not boot inside a node. The node's set is: the workflow's and the node's `mcp:`
blocks, the project's `.mcp.json` (the workflow's `autoload_project`, on by
default), the servers of enabled plugins, and iterion's own board and ask-user
servers. `ITERION_CLAUDE_CODE_STRICT_MCP=0` restores the host config; it is an
opt-in, because undeclared tools and a server boot per node visit are
usually not what a bot wants. `pi` and `claw` are strict by construction.

**2. Servers of enabled plugins arrive in every node, on their own, and may be
missing without a word.** A server contributed by an enabled plugin
(`iterion plugin list` shows which are enabled) needs no declaration: it is
*ambient* and is handed to every node that does not opt out
(`mcp: disable: [name]` on a node, or `inherit: false`). If such a server fails
to start — a missing credential, a binary not on the runner — the node does
**not** fail. It runs without those tools, and the run record may say nothing:
on `claude_code` the CLI itself skips a server it cannot start; on `claw` the
run gets an `mcp_server_degraded` event with `source: ambient`. A model that
quietly lacks the tool tends to answer from memory.

**3. Name the tool, and a failure becomes loud.** A tool the node lists
explicitly — `mcp.bars.bars_rag_search` — is a declared dependency: if the
server is dead the node fails at resolution instead of running without it. Use
it for any server the answer depends on.

**4. …but on `claude_code` a `tools:` list narrows the built-in tools.** Every
native tool the list does not name is removed from the node: with
`tools: [read_file, grep, mcp.bars.*]` it has no `Bash`, no `Edit`, no `Write`,
no `WebFetch`. MCP tools are on no roster and survive any list. So list the
built-ins the node needs next to the MCP ones, as the recipe does with
`read_file` and `grep`. `iterion validate --explain` prints, per node, exactly
what the list keeps and what it takes away. A node with **no** `tools:` line
keeps everything and gets no loud failure from rule 3. See
[what a `tools:` list costs](permissions.md#what-a-tools-list-costs-measured).

**5. A stdio server and a sandbox do not mix.** On `claude_code` a declared stdio
server is started by the CLI itself, so in a sandboxed node it runs **inside the
container**, where your host path to `bars-mcp.exe` does not exist. On `claw`
the host starts servers, and under a sandbox it starts only the operator's own
(a built-in, or a plugin the operator installed and enabled). Two ways out: set
`sandbox: none` on the workflow (as the recipe does — the validator reminds you
with `C128` that the node then runs on the host with your credentials), or serve
the tool over `http`/`sse`, which a container can reach. Details:
[MCP servers under a sandbox](sandbox.md#mcp-servers-under-a-sandbox).

## What `validate` checks

`iterion validate` and `POST /api/validate` read every `mcp.<server>.<tool>`
in a node's `tools:` against the servers the host knows: the `.bot`'s
`mcp_server` blocks, the project's `.mcp.json`, and the enabled plugins. A name
that matches none raises `C337`:

```
warning [C337]: node "ask": tool "mcp.barz.bars_read" names MCP server "barz",
but no `mcp_server` block, `.mcp.json` entry or enabled plugin declares it;
known servers: bars, … — at run time the node fails to build or runs without the tool
```

It is a warning, not an error, because the catalogue is the host's: the same
file is clean where the server exists. It checks the **server name**, never
that the tool exists on the server or that the server starts. The cloud studio
does not run it (there is no per-tenant catalogue).

A `.mcp.json` entry (or a plugin's server) whose name is in iterion's reserved
internal namespace — any name starting with `iterion`, such as `iterion` or
`iterion_board` — is **skipped with a warning**, not a failure: the other
servers are still resolved and `C337` still runs over them, in the studio's
`/api/validate`, in `iterion validate` and in a run (where the skip is a line
in the run's log — the studio's `run.log` too, on launch and on resume). The warning names the entry and says how to fix it:

```
warning: MCP server "iterion" from .mcp.json is skipped: its name is in
iterion's reserved internal namespace (a name starting with "iterion" is the
engine's own) — rename it to a name that does not start with "iterion" (for
example "engine") where it is declared; the other servers are still resolved
and checked
```

Rename the key and the warning goes away. A `tools:` or `mcp: servers:` entry
that names the skipped server still fails as *unknown MCP server*, with the same
explanation appended. A server **the `.bot` itself declares** (`mcp_server`)
under a reserved name is different: its author chose the name, so the catalogue
is refused as before. When the catalogue cannot be built for another reason (a
malformed `.mcp.json`, an invalid transport), the studio's `/api/validate`
answers *MCP catalog could not be resolved, tool servers were not checked* as a
warning and `C337` is skipped, so read that line; `iterion validate` treats that
failure as an error.

## Troubleshooting

| Symptom | Likely rule |
|---|---|
| The agent says it has no such tool, or answers without searching | The server is not in the node's set: not declared (1), or an ambient plugin server that failed to start (2). Name its tools in `tools:` to turn the silence into a failure (3) |
| The node fails at build: *unknown tool* / refused server | Rule 3 doing its job: the server is dead or the name is wrong. `C337` shows the typo before the run |
| The agent lost `Bash`, `Edit`, `WebFetch` after you added `tools:` | Rule 4: list the built-ins you still want |
| Works with `sandbox: none`, fails in the sandbox | Rule 5 |
| `iterion plugin list` shows the plugin but the tools are absent | Plugin enabled for the operator but its server needs a credential the runner lacks (2) |

## Related

- [DSL: MCP servers](dsl.md#mcp-servers) — the declaration forms and `auth:`.
- [Backends: claude_code](backends.md#claude_code) — the strict config and how
  unnamed servers degrade.
- [Plugins](plugins.md) — where ambient servers come from.
