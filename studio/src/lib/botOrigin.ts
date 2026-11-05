import type { BotEntry, BotEntryWithSchema } from "@/api/bots";

/** Where a bot in the unified list comes from. One vocabulary for every
 *  screen (Bots page, Home panel, bot pickers, Dispatcher settings). */
export type BotSource = "project" | "builtin" | "file" | "team" | "platform";

/** Origins that have no manifest: they cannot be routed by name through the
 *  catalog, have no bot page, and have no `enabled` flag to flip. */
export function isManifestless(b: Pick<BotEntry, "origin">): boolean {
  return b.origin === "builtin" || b.origin === "file";
}

export function botSource(b: Pick<BotEntry, "origin">): BotSource {
  switch (b.origin) {
    case "builtin":
      return "builtin";
    case "file":
      return "file";
    case "tenant":
      return "team";
    case "platform":
      return "platform";
    default:
      return "project";
  }
}

export const BOT_SOURCE_LABEL: Record<BotSource, string> = {
  project: "Project",
  builtin: "Built-in",
  file: "Project file",
  team: "Team",
  platform: "Platform",
};

export const BOT_SOURCE_HINT: Record<BotSource, string> = {
  project: "A bot bundle (manifest.yaml + main.bot) discovered in bots/, examples/ or .botz/ of this project.",
  builtin: "A recipe shipped inside the iterion binary. It has no manifest, so it cannot be disabled.",
  file: "A single .bot file in the project without a manifest. It runs as a plain workflow, not as a catalog bot.",
  team: "A bot authored by your team in the cloud editor.",
  platform: "A deployment-wide bot pushed by a platform administrator.",
};

/** Explains the catalog `enabled` flag wherever it is shown. */
export const BOT_ENABLED_HELP =
  "A disabled bot is out of the catalog and automatic routing: Nexie does not offer it, the board's bot picker hides it, " +
  "and the dispatcher will not route cards to it. It stays in this list so you can switch it back on (Manage on the Home bots panel), " +
  "and you can still launch it by hand. Built-in recipes and single .bot files have no manifest, so they are always enabled.";

/** The value a picker stores for a bot: the catalog name for a manifest bot,
 *  the absolute file path for a manifest-less file (the dispatcher accepts a
 *  path there), the embedded name for a built-in. */
export function botPickerValue(b: BotEntry): string {
  return b.origin === "file" ? b.path : b.name;
}

/** Workflow-path value stored in dispatcher.json for a bot: the path the
 *  dispatcher resolves directly. A bundle is stored as its directory; a
 *  built-in has no on-disk path (the dispatcher materialises it by name) and
 *  yields null. */
export function botWorkflowPath(b: BotEntry): string | null {
  if (b.origin === "builtin") return null;
  return b.path;
}

export interface BotGroup<T extends BotEntry = BotEntryWithSchema> {
  source: BotSource;
  label: string;
  bots: T[];
}

/** Groups bots for a picker: project bots first, then team/platform, then
 *  built-ins, then loose project files — the "Project files" group the
 *  owner asked for is always its own group. Empty groups are dropped. */
export function groupBotsBySource<T extends BotEntry>(bots: T[]): BotGroup<T>[] {
  const order: BotSource[] = ["project", "team", "platform", "builtin", "file"];
  return order
    .map((source) => ({
      source,
      label: source === "file" ? "Project files" : source === "builtin" ? "Built-in" : BOT_SOURCE_LABEL[source],
      bots: bots.filter((b) => botSource(b) === source),
    }))
    .filter((g) => g.bots.length > 0);
}
