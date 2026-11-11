import { useEffect, useMemo, useState } from "react";

import * as dispatcher from "@/api/dispatcher";
import { Button } from "@/components/ui/Button";
import { Checkbox } from "@/components/ui/Checkbox";
import { Textarea } from "@/components/ui/Textarea";
import { Drawer } from "@/components/ui/Drawer";
import { IconButton } from "@/components/ui/IconButton";
import { InlineBanner } from "@/components/ui/InlineBanner";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { Spinner } from "@/components/ui/Spinner";
import { useAsyncAction } from "@/hooks/useAsyncAction";
import { useConfirm } from "@/hooks/useConfirm";
import { Combobox, type ComboboxOption } from "@/components/ui/Combobox";
import { BOT_SOURCE_LABEL, botSource, botWorkflowPath, groupBotsBySource } from "@/lib/botOrigin";
import { useAllBots, useBotsStore } from "@/store/bots";

interface Props {
  open: boolean;
  onClose: () => void;
  onSaved: () => void;
}

// SettingsDrawer is the form-driven editor for dispatcher.json. It
// loads the current config from /api/v1/dispatcher/config on open and
// PUTs the result on save. No YAML editing required.
export default function SettingsDrawer({ open, onClose, onSaved }: Props) {
  const [cfg, setCfg] = useState<dispatcher.DispatcherConfig | null>(null);
  // True when the server has no dispatcher.json yet (GET /config returns
  // 404): the state in which "Use default settings" is offered. With a
  // saved config the same endpoint needs ?replace=1 (409 otherwise), which
  // is what "Reset to default settings" asks for.
  const [noConfig, setNoConfig] = useState(false);
  // Bumped on every load: the list editors of the form keep their rows in local
  // state, so a form built from an earlier load (before a reset to defaults, say)
  // must be remounted, not just handed the new settings.
  const [formKey, setFormKey] = useState(0);
  const defaultsAction = useAsyncAction();
  const { confirm, dialog: confirmDialog } = useConfirm();
  // The bot list the Workflow path / assignee pickers draw from.
  const botsLoaded = useBotsStore((s) => s.bots !== null);
  const fetchBots = useBotsStore((s) => s.fetch);
  useEffect(() => {
    if (open && !botsLoaded) void fetchBots();
  }, [open, botsLoaded, fetchBots]);
  // Two useAsyncAction slots: one for the open-time load, one for the
  // user-triggered Save. They share the same InlineBanner so the user
  // sees the most recent failure, but their `busy` flags are
  // independent — the header pill reflects load, the Save button
  // reflects save.
  const loadAction = useAsyncAction();
  const saveAction = useAsyncAction();
  const loading = loadAction.busy;
  const saving = saveAction.busy;
  const error = saveAction.error ?? loadAction.error ?? defaultsAction.error;

  useEffect(() => {
    if (!open) return;
    let alive = true;
    void loadAction.run(async () => {
      const c = await dispatcher.getConfig();
      if (alive) {
        setNoConfig(c === null);
        setCfg(c ?? defaultConfig());
        setFormKey((k) => k + 1);
      }
    });
    return () => {
      alive = false;
    };
    // loadAction is recreated on each render but its run/setError are
    // stable; depending only on `open` matches the prior behaviour
    // (the IIFE-style load fired exactly on open transitions).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const onSave = () =>
    saveAction.run(async () => {
      if (!cfg) return;
      await dispatcher.saveConfig(cfg);
      onSaved();
      onClose();
    });

  const onUseDefaults = () =>
    defaultsAction.run(async () => {
      await dispatcher.applyDefaults();
      onSaved();
      onClose();
    });

  // The way back when a config already exists: replaces the saved settings
  // (hooks included) with the ready-made ones, after an explicit confirm.
  const onResetToDefaults = async () => {
    const ok = await confirm({
      title: "Reset dispatcher settings?",
      message:
        "Replace the saved dispatcher settings with the default ones? " +
        "Your current settings, including hooks, will be overwritten and the dispatcher restarted.",
      confirmLabel: "Reset",
      confirmVariant: "danger",
    });
    if (!ok) return;
    void defaultsAction.run(async () => {
      await dispatcher.applyDefaults({ replace: true });
      onSaved();
      onClose();
    });
  };

  return (
    <>
    <Drawer
      open={open}
      onOpenChange={(o) => !o && onClose()}
      title="Dispatcher settings"
      widthClass="max-w-3xl"
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={!cfg}
            loading={saving}
            onClick={() => void onSave()}
          >
            Save
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-sm">
        {loading && (
          <span
            className="inline-flex items-center gap-1.5 text-xs text-fg-muted"
            aria-live="polite"
          >
            <Spinner size="sm" />
            <span>loading…</span>
          </span>
        )}

        {error && <InlineBanner tone="danger">{error}</InlineBanner>}

        {noConfig && !loading && (
          <InlineBanner tone="info" title="No dispatcher configured yet">
            <p>
              Fill in the form below, or let iterion create a ready-made dispatcher: it runs the
              bots of this project (the <code>bots/</code> folder) from the local board, and starts
              at once. With no bots yet, cards wait marked “no bot” until you add one.
            </p>
            <Button
              variant="secondary"
              size="sm"
              className="mt-2"
              loading={defaultsAction.busy}
              onClick={() => void onUseDefaults()}
            >
              Use default settings
            </Button>
          </InlineBanner>
        )}

        {!noConfig && !loading && cfg && (
          <InlineBanner tone="info" title="Something wrong with these settings?">
            <p>
              Go back to the ready-made dispatcher: your project&apos;s bots, the local board, the
              default hooks for this computer, started at once. The saved settings are overwritten.
            </p>
            <Button
              variant="secondary"
              size="sm"
              className="mt-2"
              loading={defaultsAction.busy}
              onClick={() => void onResetToDefaults()}
            >
              Reset to default settings
            </Button>
          </InlineBanner>
        )}

        {cfg && <Form key={formKey} cfg={cfg} setCfg={setCfg} />}
      </div>
    </Drawer>
    {confirmDialog}
    </>
  );
}

function defaultConfig(): dispatcher.DispatcherConfig {
  return {
    name: "",
    workflow: "",
    // A new dispatcher runs only cards assigned to a bot of the project.
    require_assignee: true,
    tracker: { kind: "native" },
    dispatch: { vars: {} },
    polling: { interval_ms: 30000 },
    agent: { max_concurrent: 2, max_retry_backoff_ms: 300000 },
    workspace: { root: "", persist: "cleanup_on_done" },
    hooks: {},
    stall: { timeout_ms: 600000 },
  };
}

// ---------------------------------------------------------------------------
// Form sections
// ---------------------------------------------------------------------------

interface FormProps {
  cfg: dispatcher.DispatcherConfig;
  setCfg: (c: dispatcher.DispatcherConfig) => void;
}

function Form({ cfg, setCfg }: FormProps) {
  const update = <K extends keyof dispatcher.DispatcherConfig>(
    k: K,
    v: dispatcher.DispatcherConfig[K],
  ) => setCfg({ ...cfg, [k]: v });

  return (
    <div className="space-y-6">
      <Section title="General">
        <Field label="Name" hint="Display label in logs and dashboards">
          <Input
            size="sm"
            value={cfg.name ?? ""}
            onChange={(e) => update("name", e.target.value)}
            placeholder="my-dispatcher"
          />
        </Field>
        <Field label="Workflow path" hint={WORKFLOW_PATH_HINT}>
          <BotWorkflowSelect
            value={cfg.workflow}
            onChange={(v) => update("workflow", v)}
            placeholder="Search bots, or type a path…"
          />
        </Field>
        <AssigneeWorkflowsField
          value={cfg.assignee_workflows ?? {}}
          onChange={(v) => update("assignee_workflows", v)}
        />
      </Section>

      <Section title="Routing">
        <label className="flex items-start gap-2 text-xs">
          <Checkbox
            className="mt-0.5"
            checked={cfg.require_assignee ?? false}
            onChange={(e) => update("require_assignee", e.target.checked)}
          />
          <span>
            <span className="font-medium text-fg-default">
              Only run cards assigned to a project bot
            </span>
            <span className="block text-fg-muted">
              A card with no assignee, or whose assignee is not one of the bots below (a person&apos;s
              name, a typo), is not run: it waits marked “no assignee” / “no bot”. Turn it off to run
              such cards on the fallback workflow above, if there is one. Changes to the bot
              folders and the fallback workflow apply when the dispatcher is stopped and started again.
            </span>
          </span>
        </label>
        <BotPathsField
          value={cfg.bots?.paths ?? []}
          onChange={(paths) => update("bots", { ...(cfg.bots ?? {}), paths })}
        />
        <AssigneeDispatchField
          value={cfg.assignee_dispatch ?? {}}
          onChange={(v) => update("assignee_dispatch", v)}
        />
      </Section>

      <Section title="Tracker">
        <Field label="Kind">
          <Select
            size="sm"
            value={cfg.tracker.kind}
            onChange={(e) => update("tracker", { ...cfg.tracker, kind: e.target.value as dispatcher.TrackerKind })}
          >
            <option value="native">native (local kanban)</option>
            <option value="github">github</option>
            <option value="forgejo">forgejo / gitea</option>
            <option value="gitlab">gitlab</option>
          </Select>
        </Field>
        {cfg.tracker.kind === "github" && (
          <GitHubTrackerFields
            cfg={cfg.tracker.github ?? { repo: "" }}
            set={(g) => update("tracker", { ...cfg.tracker, github: g })}
          />
        )}
        {cfg.tracker.kind === "forgejo" && (
          <ForgejoTrackerFields
            cfg={cfg.tracker.forgejo ?? { host: "", repo: "" }}
            set={(f) => update("tracker", { ...cfg.tracker, forgejo: f })}
          />
        )}
        {cfg.tracker.kind === "gitlab" && (
          <GitLabTrackerFields
            cfg={cfg.tracker.gitlab ?? { host: "", repo: "" }}
            set={(g) => update("tracker", { ...cfg.tracker, gitlab: g })}
          />
        )}
      </Section>

      <Section title="Dispatch — workflow vars">
        <p className="text-xs text-fg-muted">
          Map each <code>vars:</code> declared in the workflow to a template. Use{" "}
          <code>{"{{issue.title}}"}</code>, <code>{"{{issue.body}}"}</code>,{" "}
          <code>{"{{dispatcher.workspace_path}}"}</code>, etc.
        </p>
        <KVTable
          rows={cfg.dispatch?.vars ?? {}}
          onChange={(vars) => update("dispatch", { ...(cfg.dispatch ?? {}), vars })}
          keyPlaceholder="feature_prompt"
          valuePlaceholder="Issue {{issue.identifier}}: {{issue.title}}"
          keyLabel="Dispatch variable name"
          valueLabel="Dispatch variable template"
        />
      </Section>

      <Section title="Polling">
        <Field
          label="Interval (ms)"
          hint="How often the dispatcher checks the tracker. Default 30 000 ms (30 s)."
        >
          <Input
            size="sm"
            type="number"
            min={1000}
            value={cfg.polling?.interval_ms ?? 30000}
            onChange={(e) => update("polling", { interval_ms: Number(e.target.value) })}
          />
        </Field>
      </Section>

      <Section title="Concurrency & retry">
        <Field label="Max concurrent dispatches">
          <Input
            size="sm"
            type="number"
            min={1}
            value={cfg.agent?.max_concurrent ?? 2}
            onChange={(e) =>
              update("agent", { ...(cfg.agent ?? {}), max_concurrent: Number(e.target.value) })
            }
          />
        </Field>
        <Field label="Max retry backoff (ms)" hint="Exponential cap. Default 300000 (5min)">
          <Input
            size="sm"
            type="number"
            min={1000}
            value={cfg.agent?.max_retry_backoff_ms ?? 300000}
            onChange={(e) =>
              update("agent", { ...(cfg.agent ?? {}), max_retry_backoff_ms: Number(e.target.value) })
            }
          />
        </Field>
      </Section>

      <Section title="Workspace">
        <Field
          label="Root"
          hint="Per-issue workspaces live here. Empty = <store-dir>/dispatcher/workspaces."
        >
          <Input
            size="sm"
            value={cfg.workspace?.root ?? ""}
            onChange={(e) => update("workspace", { ...(cfg.workspace ?? {}), root: e.target.value })}
            placeholder="~/.iterion/dispatcher-workspaces"
          />
        </Field>
        <Field label="Persistence">
          <Select
            size="sm"
            value={cfg.workspace?.persist ?? ""}
            onChange={(e) =>
              update("workspace", {
                ...(cfg.workspace ?? {}),
                persist: e.target.value as dispatcher.WorkspacePersistPolicy,
              })
            }
          >
            <option value="">keep (manual cleanup)</option>
            <option value="cleanup_on_done">cleanup on successful run</option>
            <option value="cleanup_on_terminal">cleanup on terminal state</option>
          </Select>
        </Field>
      </Section>

      <Section title="Hooks">
        <p className="text-xs text-fg-muted">
          Inline shell snippets that run with cwd = workspace. Leave blank to skip.
          Use them to clone the issue&apos;s source, seed credentials, or run a
          teardown.
        </p>
        <HookFields
          label="after_create"
          value={cfg.hooks?.after_create ?? null}
          onChange={(h) => update("hooks", { ...(cfg.hooks ?? {}), after_create: h })}
        />
        <HookFields
          label="before_run"
          value={cfg.hooks?.before_run ?? null}
          onChange={(h) => update("hooks", { ...(cfg.hooks ?? {}), before_run: h })}
        />
        <HookFields
          label="after_run"
          value={cfg.hooks?.after_run ?? null}
          onChange={(h) => update("hooks", { ...(cfg.hooks ?? {}), after_run: h })}
        />
        <HookFields
          label="before_remove"
          value={cfg.hooks?.before_remove ?? null}
          onChange={(h) => update("hooks", { ...(cfg.hooks ?? {}), before_remove: h })}
        />
      </Section>

      <Section title="Stall detection">
        <Field
          label="Timeout (ms)"
          hint="If a run is silent for this long, the dispatcher cancels and retries it. 0 (or any negative value) disables stall detection; the default is 600 000 ms."
        >
          <Input
            size="sm"
            type="number"
            min={-1}
            value={cfg.stall?.timeout_ms ?? 600000}
            onChange={(e) => update("stall", { timeout_ms: Number(e.target.value) })}
          />
        </Field>
      </Section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2 rounded border border-border-default bg-surface-1 p-3">
      <h3 className="text-xs font-semibold uppercase tracking-wide text-fg-muted">{title}</h3>
      <div className="space-y-3">{children}</div>
    </section>
  );
}

const WORKFLOW_PATH_HINT =
  "Fallback for a card that names no bot — used only when “Only run cards assigned to a " +
  "project bot” (Routing) is off, and may be left empty. Pick a bot: the studio fills in its " +
  "full path for you. A path typed by hand (.bot, .botz or an unpacked bundle folder) may be " +
  "relative: it is resolved against the project root.";

/** BotWorkflowSelect is the searchable replacement for a workflow-path text
 *  box: the operator picks a bot by name and the studio stores its full path.
 *  freeSolo keeps a hand-typed path possible (an explicit operator choice is
 *  never replaced). Disabled catalog bots are listed but marked. */
function BotWorkflowSelect({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (path: string) => void;
  placeholder?: string;
}) {
  const bots = useAllBots();
  const options = useMemo<ComboboxOption<string>[]>(() => {
    const out: ComboboxOption<string>[] = [];
    for (const g of groupBotsBySource(bots ?? [])) {
      for (const b of g.bots) {
        const path = botWorkflowPath(b);
        const persona = b.display_name?.trim();
        const disabled = b.enabled === false;
        out.push({
          value: path,
          label: `${persona || b.name}${disabled ? " (disabled)" : ""}`,
          description: path,
          meta: botSource(b) === "project" ? undefined : BOT_SOURCE_LABEL[botSource(b)],
          searchHaystack: `${persona ?? ""} ${b.name} ${b.rel_path ?? ""} ${BOT_SOURCE_LABEL[botSource(b)]}`,
        });
      }
    }
    return out;
  }, [bots]);
  return (
    <Combobox<string>
      value={value}
      options={options}
      onChange={(v) => onChange(v)}
      placeholder={placeholder}
      freeSolo
    />
  );
}

/** AssigneeWorkflowsField edits assignee_workflows: a card assigned to the
 *  name on the left runs the bot on the right instead of the default.
 *  Rows live in local state (an array, so a half-typed or duplicate name and
 *  a numeric-looking name keep their place); the config object is rebuilt
 *  from the complete rows only — the server rejects an empty key or path. */
function AssigneeWorkflowsField({
  value,
  onChange,
}: {
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}) {
  const [rows, setRows] = useState<{ k: string; v: string }[]>(() =>
    Object.entries(value).map(([k, v]) => ({ k, v })),
  );
  const commit = (next: { k: string; v: string }[]) => {
    setRows(next);
    const out: Record<string, string> = {};
    for (const r of next) {
      const k = r.k.trim();
      if (k && r.v) out[k] = r.v;
    }
    onChange(out);
  };
  const dup = (k: string) => k.trim() !== "" && rows.filter((r) => r.k.trim() === k.trim()).length > 1;
  return (
    <div className="space-y-2">
      <span className="text-xs font-medium text-fg-default">Per-assignee bots</span>
      <span className="block text-xs text-fg-muted">
        A card assigned to the name on the left runs the chosen bot instead of the default one.
        A row with an empty name or bot is not saved.
      </span>
      {rows.map((r, i) => (
        <div key={i} className="flex items-start gap-2">
          <div className="w-36">
            <Input
              size="sm"
              value={r.k}
              aria-label={`Assignee name for row ${i + 1}`}
              placeholder="assignee"
              onChange={(e) => commit(rows.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))}
            />
            {dup(r.k) && <span className="text-micro text-warning-fg">duplicate name</span>}
          </div>
          <div className="flex-1">
            <BotWorkflowSelect
              value={r.v}
              onChange={(v) => commit(rows.map((x, j) => (j === i ? { ...x, v } : x)))}
              placeholder="Search bots…"
            />
          </div>
          <IconButton
            size="sm"
            variant="danger"
            label={r.k ? `Remove ${r.k}` : "Remove row"}
            onClick={() => commit(rows.filter((_, j) => j !== i))}
          >
            ×
          </IconButton>
        </div>
      ))}
      <Button variant="ghost" size="sm" onClick={() => setRows([...rows, { k: "", v: "" }])}>
        + Add assignee bot
      </Button>
    </div>
  );
}

/** BotPathsField edits bots.paths: the folders the dispatcher discovers bots in
 *  (a relative path is taken from the project root). Empty = the project's
 *  bots/, examples/ and .botz/. Rows live in local state so a half-typed path
 *  keeps its place; empty rows are not saved. */
function BotPathsField({
  value,
  onChange,
}: {
  value: string[];
  onChange: (v: string[]) => void;
}) {
  const [rows, setRows] = useState<string[]>(() => [...value]);
  const commit = (next: string[]) => {
    setRows(next);
    onChange(next.map((p) => p.trim()).filter(Boolean));
  };
  return (
    <div className="space-y-2">
      <span className="text-xs font-medium text-fg-default">Bot folders (bots.paths)</span>
      <span className="block text-xs text-fg-muted">
        Where the dispatcher looks for bots. A relative path is taken from the project root. Leave
        empty for the project&apos;s <code>bots/</code>, <code>examples/</code> and{" "}
        <code>.botz/</code>.
      </span>
      {rows.map((p, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            size="sm"
            className="flex-1"
            value={p}
            aria-label={`Bot folder ${i + 1}`}
            placeholder="./bots"
            onChange={(e) => commit(rows.map((x, j) => (j === i ? e.target.value : x)))}
          />
          <IconButton
            size="sm"
            variant="danger"
            label={p ? `Remove ${p}` : "Remove row"}
            onClick={() => commit(rows.filter((_, j) => j !== i))}
          >
            ×
          </IconButton>
        </div>
      ))}
      <Button variant="ghost" size="sm" onClick={() => setRows([...rows, ""])}>
        + Add bot folder
      </Button>
    </div>
  );
}

/** AssigneeDispatchField edits assignee_dispatch: input vars for one bot that
 *  replace the bot's own manifest `dispatch_vars` (and the global vars). Most
 *  bots need no row — their manifest is read at every dispatch. Rows live in
 *  local state; a row with no name is not saved; fields the form does not edit
 *  (attachments) stay on their entry. */
function AssigneeDispatchField({
  value,
  onChange,
}: {
  value: Record<string, dispatcher.AssigneeDispatchConfig>;
  onChange: (v: Record<string, dispatcher.AssigneeDispatchConfig>) => void;
}) {
  type Row = { k: string; entry: dispatcher.AssigneeDispatchConfig };
  const [rows, setRows] = useState<Row[]>(() =>
    Object.entries(value).map(([k, entry]) => ({ k, entry })),
  );
  const commit = (next: Row[]) => {
    setRows(next);
    const out: Record<string, dispatcher.AssigneeDispatchConfig> = {};
    for (const r of next) {
      const k = r.k.trim();
      if (k) out[k] = r.entry;
    }
    onChange(out);
  };
  return (
    <div className="space-y-2">
      <span className="text-xs font-medium text-fg-default">Per-bot input vars</span>
      <span className="block text-xs text-fg-muted">
        Overrides the <code>dispatch_vars</code> of the bot&apos;s manifest for cards run by that
        bot. Leave empty to use the manifest. The name must be a bot of this project.
      </span>
      {rows.map((r, i) => (
        <div key={i} className="space-y-2 rounded border border-border-default bg-surface-2 p-2">
          <div className="flex items-center gap-2">
            <Input
              size="sm"
              className="flex-1"
              value={r.k}
              aria-label={`Bot name for vars row ${i + 1}`}
              placeholder="bot name"
              onChange={(e) => commit(rows.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))}
            />
            <IconButton
              size="sm"
              variant="danger"
              label={r.k ? `Remove ${r.k}` : "Remove row"}
              onClick={() => commit(rows.filter((_, j) => j !== i))}
            >
              ×
            </IconButton>
          </div>
          <KVTable
            rows={r.entry.vars ?? {}}
            onChange={(vars) =>
              commit(rows.map((x, j) => (j === i ? { ...x, entry: { ...x.entry, vars } } : x)))
            }
            keyPlaceholder="task"
            valuePlaceholder="{{issue.title}}: {{issue.body}}"
            keyLabel={`Per-bot variable name (row ${i + 1})`}
            valueLabel={`Per-bot variable template (row ${i + 1})`}
          />
        </div>
      ))}
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setRows([...rows, { k: "", entry: { vars: {} } }])}
      >
        + Add per-bot vars
      </Button>
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-fg-default">{label}</span>
      {children}
      {hint && <span className="block text-xs text-fg-muted">{hint}</span>}
    </label>
  );
}

function GitHubTrackerFields({
  cfg,
  set,
}: {
  cfg: dispatcher.GitHubTrackerConfig;
  set: (c: dispatcher.GitHubTrackerConfig) => void;
}) {
  return (
    <>
      <Field label="Repo" hint="owner/repo">
        <Input size="sm" value={cfg.repo} onChange={(e) => set({ ...cfg, repo: e.target.value })} placeholder="SocialGouv/iterion" />
      </Field>
      <Field label="Token" hint="Env var name (e.g. $GITHUB_TOKEN) or literal. Leave blank to use `gh auth token`.">
        <Input size="sm" value={cfg.token ?? ""} onChange={(e) => set({ ...cfg, token: e.target.value })} placeholder="$GITHUB_TOKEN" />
      </Field>
      <Field label="Claimed label" hint="Label added to claimed issues. Default 'iterion-claimed'.">
        <Input size="sm" value={cfg.claimed_label ?? ""} onChange={(e) => set({ ...cfg, claimed_label: e.target.value })} placeholder="iterion-claimed" />
      </Field>
      <LabelListField label="Include labels (AND filter)" value={cfg.include_labels ?? []} onChange={(v) => set({ ...cfg, include_labels: v })} />
      <LabelListField label="Exclude labels" value={cfg.exclude_labels ?? []} onChange={(v) => set({ ...cfg, exclude_labels: v })} />
      <StateMappingField value={cfg.state_mapping ?? {}} onChange={(v) => set({ ...cfg, state_mapping: v })} />
    </>
  );
}

function ForgejoTrackerFields({
  cfg,
  set,
}: {
  cfg: dispatcher.ForgejoTrackerConfig;
  set: (c: dispatcher.ForgejoTrackerConfig) => void;
}) {
  return (
    <>
      <Field label="Host" hint="Base URL, e.g. https://codeberg.org">
        <Input size="sm" value={cfg.host} onChange={(e) => set({ ...cfg, host: e.target.value })} placeholder="https://codeberg.org" />
      </Field>
      <Field label="Repo" hint="owner/repo">
        <Input size="sm" value={cfg.repo} onChange={(e) => set({ ...cfg, repo: e.target.value })} placeholder="forgejo/forgejo" />
      </Field>
      <Field label="Token">
        <Input size="sm" value={cfg.token ?? ""} onChange={(e) => set({ ...cfg, token: e.target.value })} placeholder="$FORGEJO_TOKEN" />
      </Field>
      <Field label="Claimed label">
        <Input size="sm" value={cfg.claimed_label ?? ""} onChange={(e) => set({ ...cfg, claimed_label: e.target.value })} placeholder="iterion-claimed" />
      </Field>
      <LabelListField label="Include labels" value={cfg.include_labels ?? []} onChange={(v) => set({ ...cfg, include_labels: v })} />
      <LabelListField label="Exclude labels" value={cfg.exclude_labels ?? []} onChange={(v) => set({ ...cfg, exclude_labels: v })} />
      <StateMappingField value={cfg.state_mapping ?? {}} onChange={(v) => set({ ...cfg, state_mapping: v })} />
    </>
  );
}

function GitLabTrackerFields({
  cfg,
  set,
}: {
  cfg: dispatcher.GitLabTrackerConfig;
  set: (c: dispatcher.GitLabTrackerConfig) => void;
}) {
  return (
    <>
      <Field label="Host" hint="Base URL, e.g. https://gitlab.com">
        <Input size="sm" value={cfg.host} onChange={(e) => set({ ...cfg, host: e.target.value })} placeholder="https://gitlab.com" />
      </Field>
      <Field label="Repo" hint="group/project">
        <Input size="sm" value={cfg.repo} onChange={(e) => set({ ...cfg, repo: e.target.value })} placeholder="gitlab-org/gitlab" />
      </Field>
      <Field label="Token">
        <Input size="sm" value={cfg.token ?? ""} onChange={(e) => set({ ...cfg, token: e.target.value })} placeholder="$GITLAB_TOKEN" />
      </Field>
      <Field label="Claimed label" hint="Label added to claimed issues. Default 'iterion-claimed'.">
        <Input size="sm" value={cfg.claimed_label ?? ""} onChange={(e) => set({ ...cfg, claimed_label: e.target.value })} placeholder="iterion-claimed" />
      </Field>
      <LabelListField label="Include labels" value={cfg.include_labels ?? []} onChange={(v) => set({ ...cfg, include_labels: v })} />
      <LabelListField label="Exclude labels" value={cfg.exclude_labels ?? []} onChange={(v) => set({ ...cfg, exclude_labels: v })} />
      <StateMappingField value={cfg.state_mapping ?? {}} onChange={(v) => set({ ...cfg, state_mapping: v })} />
    </>
  );
}

function LabelListField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string[];
  onChange: (v: string[]) => void;
}) {
  return (
    <Field label={label} hint="Comma-separated">
      <Input
        size="sm"
        value={value.join(", ")}
        onChange={(e) =>
          onChange(
            e.target.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          )
        }
      />
    </Field>
  );
}

function StateMappingField({
  value,
  onChange,
}: {
  value: Record<string, dispatcher.LabelSelector>;
  onChange: (v: Record<string, dispatcher.LabelSelector>) => void;
}) {
  const entries = Object.entries(value);
  const setKey = (oldKey: string, newKey: string) => {
    const next: Record<string, dispatcher.LabelSelector> = {};
    for (const [k, v] of entries) {
      next[k === oldKey ? newKey : k] = v;
    }
    onChange(next);
  };
  const setSelector = (key: string, sel: dispatcher.LabelSelector) => {
    onChange({ ...value, [key]: sel });
  };
  const remove = (key: string) => {
    const next = { ...value };
    delete next[key];
    onChange(next);
  };
  return (
    <div className="space-y-2">
      <span className="text-xs font-medium">State mapping (workflow state → label selector)</span>
      {entries.map(([state, sel]) => (
        <div key={state} className="space-y-1 rounded border border-border-default bg-surface-2 p-2">
          <Input
            size="sm"
            value={state}
            onChange={(e) => setKey(state, e.target.value)}
            placeholder="ready"
          />
          <Input
            size="sm"
            placeholder="includes (comma-separated)"
            value={(sel.labels_include ?? []).join(", ")}
            onChange={(e) =>
              setSelector(state, {
                ...sel,
                labels_include: e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
              })
            }
          />
          <Input
            size="sm"
            placeholder="excludes (comma-separated)"
            value={(sel.labels_exclude ?? []).join(", ")}
            onChange={(e) =>
              setSelector(state, {
                ...sel,
                labels_exclude: e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
              })
            }
          />
          <Button
            variant="ghost"
            size="sm"
            className="text-danger hover:text-danger"
            onClick={() => remove(state)}
          >
            Remove
          </Button>
        </div>
      ))}
      <Button
        variant="ghost"
        size="sm"
        onClick={() =>
          onChange({ ...value, "": { labels_include: [], labels_exclude: [] } })
        }
      >
        + Add state mapping
      </Button>
    </div>
  );
}

// A workflow var name is an identifier, like the DSL's `vars:` (a letter or
// underscore, then letters, digits, underscores). Anything else — typically a
// `{{...}}` template typed into the name field — is warned about, never refused.
const VAR_NAME_RE = /^[\p{L}_][\p{L}\p{N}_]*$/u;

function varNameWarning(name: string): string | null {
  if (name === "") return "Variable name is empty.";
  if (name.includes("{{"))
    return "This looks like a template. The name goes in the first field, the template in the second.";
  if (!VAR_NAME_RE.test(name))
    return "A variable name should be an identifier: letters, digits and underscores, not starting with a digit.";
  return null;
}

function KVTable({
  rows,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
  keyLabel = "Variable name",
  valueLabel = "Template",
}: {
  rows: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  keyLabel?: string;
  valueLabel?: string;
}) {
  const entries = Object.entries(rows);
  const setKey = (oldKey: string, newKey: string) => {
    const next: Record<string, string> = {};
    for (const [k, v] of entries) next[k === oldKey ? newKey : k] = v;
    onChange(next);
  };
  const setVal = (k: string, val: string) => onChange({ ...rows, [k]: val });
  const remove = (k: string) => {
    const next = { ...rows };
    delete next[k];
    onChange(next);
  };
  return (
    <div className="space-y-2">
      {entries.length > 0 && (
        <div className="flex gap-2 text-xs text-fg-muted">
          <span className="w-32">Variable name</span>
          <span className="flex-1">Template</span>
        </div>
      )}
      {entries.map(([k, v], i) => {
        const warning = varNameWarning(k);
        return (
          // Keyed by position: the name is editable, so keying by it would
          // remount the row (and drop focus) on every keystroke.
          <div key={i} className="space-y-1">
            <div className="flex gap-2">
              <Input
                size="sm"
                className="w-32"
                value={k}
                aria-label={keyLabel}
                placeholder={keyPlaceholder}
                onChange={(e) => setKey(k, e.target.value)}
              />
              <Textarea
                className="flex-1 min-h-[2.25rem]"
                rows={1}
                value={v}
                aria-label={valueLabel}
                placeholder={valuePlaceholder}
                onChange={(e) => setVal(k, e.target.value)}
              />
              <IconButton
                size="sm"
                variant="danger"
                label={k ? `Remove ${k}` : "Remove row"}
                onClick={() => remove(k)}
              >
                ×
              </IconButton>
            </div>
            {warning && (
              <p role="alert" className="text-xs text-warning-fg">
                {warning}
              </p>
            )}
          </div>
        );
      })}
      <Button
        variant="ghost"
        size="sm"
        onClick={() => onChange({ ...rows, "": "" })}
      >
        + Add var
      </Button>
    </div>
  );
}

function HookFields({
  label,
  value,
  onChange,
}: {
  label: string;
  value: dispatcher.HookSpec | null;
  onChange: (h: dispatcher.HookSpec | null) => void;
}) {
  const enabled = value != null;
  const toggle = (on: boolean) => {
    if (on) onChange({ script: "", timeout_ms: 60000 });
    else onChange(null);
  };
  return (
    <div className="space-y-1 rounded border border-border-default bg-surface-2 p-2">
      <label className="flex items-center gap-2 text-xs">
        <Checkbox checked={enabled} onChange={(e) => toggle(e.target.checked)} />
        <span className="font-mono">{label}</span>
      </label>
      {enabled && value && (
        <>
          <Textarea
            className="min-h-[3rem] font-mono text-xs"
            rows={3}
            value={value.script ?? ""}
            placeholder="git clone $REPO ."
            onChange={(e) => onChange({ ...value, script: e.target.value, path: undefined })}
          />
          <div className="flex gap-2">
            <Input
              size="sm"
              className="flex-1"
              placeholder="… or path to a script"
              value={value.path ?? ""}
              onChange={(e) => onChange({ ...value, path: e.target.value, script: undefined })}
            />
            <Input
              size="sm"
              className="w-32"
              type="number"
              min={1000}
              value={value.timeout_ms ?? 60000}
              onChange={(e) => onChange({ ...value, timeout_ms: Number(e.target.value) })}
            />
          </div>
        </>
      )}
    </div>
  );
}
