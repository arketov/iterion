import type { BotEntry, BotEntryWithSchema } from "@/api/bots";

/** Where a bot in the unified list comes from. One vocabulary for every
 *  screen (Bots page, Home panel, bot pickers, Dispatcher settings). */
export type BotSource = "project" | "file" | "team" | "platform";

/** A single .bot file has no manifest: it cannot be routed by name through
 *  the catalog, has no bot page, and has no `enabled` flag to flip. */
export function isManifestless(b: Pick<BotEntry, "origin">): boolean {
  return b.origin === "file";
}

export function botSource(b: Pick<BotEntry, "origin">): BotSource {
  switch (b.origin) {
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
  file: "Project file",
  team: "Team",
  platform: "Platform",
};

export const BOT_SOURCE_HINT: Record<BotSource, string> = {
  project: "A bot bundle (manifest.yaml + main.bot) discovered in bots/, examples/ or .botz/ of this project.",
  file: "A single .bot file in the project without a manifest. It runs as a plain workflow, not as a catalog bot.",
  team: "A bot authored by your team in the cloud editor.",
  platform: "A deployment-wide bot pushed by a platform administrator.",
};

/** Explains the catalog `enabled` flag wherever it is shown. */
export const BOT_ENABLED_HELP =
  "A disabled bot is out of the catalog and automatic routing: Nexie does not offer it, the board's bot picker hides it, " +
  "and the dispatcher will not route cards to it. It stays in this list so you can switch it back on (Manage on the Home bots panel), " +
  "and you can still launch it by hand. Single .bot files have no manifest, so they are always enabled.";

/** The value a picker stores for a bot: the catalog name for a manifest bot,
 *  the absolute file path for a manifest-less file (the dispatcher accepts a
 *  path there). */
export function botPickerValue(b: BotEntry): string {
  return b.origin === "file" ? b.path : b.name;
}

/** Workflow-path value stored in dispatcher.json for a bot: the path the
 *  dispatcher resolves directly (a bundle is its directory, a file its .bot). */
export function botWorkflowPath(b: BotEntry): string {
  return b.path;
}

export interface BotGroup<T extends BotEntry = BotEntryWithSchema> {
  source: BotSource;
  label: string;
  bots: T[];
}

/** Groups bots for a picker: project bots first, then team/platform, then
 *  loose project files — the "Project files" group the
 *  owner asked for is always its own group. Empty groups are dropped. */
export function groupBotsBySource<T extends BotEntry>(bots: T[]): BotGroup<T>[] {
  const order: BotSource[] = ["project", "team", "platform", "file"];
  return order
    .map((source) => ({
      source,
      label: source === "file" ? "Project files" : BOT_SOURCE_LABEL[source],
      bots: bots.filter((b) => botSource(b) === source),
    }))
    .filter((g) => g.bots.length > 0);
}

export interface BotCounts {
  /** Catalog bots: what every screen calls "bots". */
  total: number;
  /** Manifest bots a workspace overlay can switch on and off (Manage). */
  catalog: number;
  /** Single .bot files without a manifest, counted apart as "project files". */
  files: number;
}

/** countBots is the one place the "Bots (N)" numbers come from, so the Home
 *  panel, the Bots page and the Manage dialog agree: N = catalog bots,
 *  project files are named separately. */
export function countBots(bots: BotEntry[] | null, extra: BotEntry[]): BotCounts {
  const catalog = bots?.length ?? 0;
  const files = extra.filter((b) => b.origin === "file").length;
  return { total: catalog, catalog, files };
}
