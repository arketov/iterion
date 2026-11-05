# 🚀 Quickstart

For developers who already live in AI coding tools. Zero to a real agent
workflow — running, watched, and landed — in a few minutes.

Already on iterion.cloud, or want your agent to do the setup? [Get started
in one prompt](getting-started.md) installs, connects, and verifies the
cloud path for you.

## 1. Install

```bash
curl -fsSL https://socialgouv.github.io/iterion/install.sh | sh
iterion version
```

No API key yet? That's fine — step 2 runs with no credentials.

## 2. Run the engine (no credentials)

Prove the pipeline works with a deterministic, LLM-free example — it compiles a
`.bot` to a graph and executes it:

```bash
iterion run examples/else_edge.bot --var n=100
```

The examples open with `dsl: 2`, the current syntax profile, so this needs
iterion ≥ 3.141.0 — an older binary stops at `E001: unexpected token 'dsl'`
(check with `iterion --version`; [docs/examples.md](examples.md) says which
examples carry the floor in a manifest).

You get a typed run: a persisted event log, versioned node artifacts, and a
final result. Inspect it:

```bash
iterion inspect --events
```

## 3. See it — and launch visually

```bash
iterion studio
```

The browser studio is the graph editor, the run console, and a Launch form in
one. Open a `.bot`, watch nodes light up live, answer human gates, and follow
cost/tokens as the run goes.

## 4. Run a real bot on your repo

Iterion **auto-detects** whatever you already have signed in — Claude Code
OAuth (the subscription "forfait"), `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, and
more (see [backends](backends.md)). With one available, point a catalogue bot at a
repo. For example, a read-only review of your current branch (one model family
by default; add `--var review_mode=dual` for cross-family confirmation):

```bash
cd /path/to/your/repo
iterion run /path/to/iterion/bots/review-pr/main.bot
```

Or ship a whole feature autonomously (runs in an isolated git worktree, on by
default):

```bash
iterion run bots/feature-dev/main.bot \
  --var feature_prompt="Add a --json flag to the export command" \
  --max-cost-usd 5
```

Everything is bounded: `--max-cost-usd`, `--max-tokens`, `--max-duration`. A run
that hits the cap pauses resumably — raise it and `iterion resume`.

## 5. Operate the run

```bash
iterion runs list                 # every run, with status
iterion report --run-id <id>      # a chronological report
iterion resume --run-id <id> --file <bot>   # continue a paused/failed run
```

A `worktree: auto` bot lands its commits on a named branch and (best-effort)
fast-forwards your checked-out branch — the studio RunHeader always shows where
the work went. Nothing touches your tree unless a guard passes.

## 6. Make it yours

The catalogue is a starting point, not a cage:

```bash
iterion bots list                 # discover the fleet
iterion bots create my-bot        # scaffold a new bundle
```

Fork any bot, edit its `main.bot` in the [studio builder](visual-editor.md), and
learn the language from the [DSL guide](dsl.md). Two step-by-step recipes for
the next things people try: [run your own bot from a card on the
board](howto-board-bot.md) and [give a node an MCP server](howto-mcp-node.md).
When you're ready to operate
agents for a team — orgs, quotas, webhooks, audit — see
[Iterion Cloud](cloud-overview.md).

## Check before you run

A run costs money; most mistakes in a new `.bot` can be found for free, in three
steps that each catch what the one before cannot:

```bash
iterion validate my.bot             # parse + compile: every error and warning, with a fix: line
iterion validate my.bot --exec      # dry run: renders every prompt and command, no model, no shell
iterion validate my.bot --explain   # per claude_code node: which built-in tools its tools: list keeps
```

- **`validate`** compiles the program. A bundle folder, a `.botz` or a lone
  `.bot` are all accepted; for a bundle it also cross-checks `manifest.yaml`
  against the workflow (`dispatch_vars` keys that name no var, and so on).
  Findings carry a code (`C128`, `C200`, `C332`); the
  [catalogue](references/diagnostics.md) says what each means.
- **`validate --exec`** is the dry run: the program is run twice under a
  simulation (every condition true, then false) with no model, no shell and no
  workspace. Each node's `{{…}}` prompts, `command:` and `script:` are rendered
  for real, anything left unresolved is named with the reason, and shell text is
  syntax-checked. It says what the first paid run would have met, and does not
  change the exit code unless you add `--strict` (for CI). Give a bot that
  expects inputs its values with `--var key=value`. Reference:
  [`iterion validate`](cli-reference.md#iterion-validate) and the
  [DSL guide](dsl.md#validation-and-references).
- **`validate --explain`** prints the tool surface of each `claude_code` node:
  what its `tools:` list keeps and what it removes. Run it after you add
  `tools:` ([why](permissions.md#what-a-tools-list-costs-measured)).

### Without `iterion` in your `PATH`: the studio's HTTP API

With only the desktop app and no `iterion` on your `PATH`, a running studio
answers the same questions over HTTP, on a loopback port that **changes at every restart**.
Find it in the per-project discovery file the daemon writes:

```text
~/.iterion/daemons/<project-key>.json      {"url": "http://127.0.0.1:61321/", "pid": …, "project_dir": "C:\…", "started_at": …}
```

The file name is the project folder with separators turned into `-`; when you
have several projects open, read `project_dir` in each file. If the daemon is not
running the file may be stale — the `pid` tells. Then two calls: `POST
/api/parse` turns source text into a document, `POST /api/validate` checks the
document.

```python
import json, os, urllib.request

info = json.load(open(os.path.expanduser("~/.iterion/daemons/<project-key>.json"), encoding="utf-8"))
base = info["url"].rstrip("/")

def post(path, body):
    req = urllib.request.Request(base + path, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=30))

source = open("my.bot", encoding="utf-8").read()
parsed = post("/api/parse", {"source": source})      # {"document": {...}, "bindable": true, "issues": [...]}
result = post("/api/validate", {"document": parsed["document"]})
print(result["valid"], [i.get("code") for i in result.get("issues", [])])
```

`/api/parse` reports syntax problems (`issues`; `bindable: false` means the
document is only what the parser could salvage). `/api/validate` returns
`valid`, `issues` (each with `code`, `severity`, `message`, `hint`, `node_id`)
and `warnings`, the same findings the CLI prints. For a `main.bot` that lives in
a bundle, add `"path": "bots/my-bot/main.bot"` (workspace-relative) so its
`prompts/*.md` are in scope. This is the compile check only; the `--exec` dry
run is a CLI feature. The full route list of the running studio is its own
`GET /api/openapi.json`.

## Name a model, and check the name

A node picks its model with `model: "<provider>/<model-id>"`:

```iter fragment
agent reviewer:
  backend: "claude_code"
  model: "anthropic/claude-sonnet-5-5"
```

The prefix is the **API dialect, not the vendor** (GLM models are
`anthropic/glm-*` and need a z.ai key; see [models](models.md#two-mappings-that-are-easy-to-get-wrong)).
iterion does not refuse a name it has never heard of, so check one before a run
pays for it:

```bash
iterion models anthropic/claude-sonnet-5-5
```

```text
MODEL                        SOURCE      CONTEXT  MAX OUT  REASON  TOOLS  TEMP  PRICE IN/OUT  USABLE
anthropic/claude-sonnet-5-5  aggregator  1M       128K     yes     yes    no    $2/$10        claude_code
```

Over HTTP, for a studio that is running:
`GET /api/models?spec=anthropic/claude-sonnet-5-5` (repeat `spec` for several).
Unlike the CLI, which prints only the model you asked about, the answer is the
whole catalogue with your specs added: find your entry in `models` by its
`spec` field (and read `invalid_specs` for a malformed one).

Read the row, not just the last column:

- **Known:** `SOURCE aggregator`, a context window and a price
  (`price_known: true` in JSON).
- **Not known:** `SOURCE curated` with `—` for context and price
  (`context_window: 0`, `price_known: false`). A misspelled id such as
  `anthropic/claude-sonnet-9-9` lands here — and **still reads usable**,
  because *usable* only says a credential for that provider is present, never
  that the provider has such a model. The run fails at its first call.
- **Unusable:** `no`, with the reason printed under the table (usually the
  credential that is missing).

`--refresh` re-fetches the price and capability table first. Everything else —
how the registry is built, reachability in the cloud, launch-time overrides —
is in [models](models.md). Older ids (`claude-sonnet-4-6`) are still in the
registry; the examples in these docs use the current ones.

---

Stuck on credentials or backend selection? [Backends & auto-detection](backends.md)
covers every path. Want the vision first? [Why Iterion?](why-iterion.md)
