import { useMemo } from "react";

import type { BotEntry } from "@/api/bots";
import { Combobox, type ComboboxOption } from "@/components/ui/Combobox";
import { botIdentity } from "@/lib/personas";
import { BOT_SOURCE_LABEL, botPickerValue, botSource, groupBotsBySource } from "@/lib/botOrigin";

interface Props {
  value: string;
  bots: BotEntry[];
  onChange: (botName: string) => void;
  disabled?: boolean;
  /** Pre-baked tags shown to the right of each row (default: triggers
   *  + capabilities, comma-joined and truncated). */
  metaFor?: (bot: BotEntry) => string;
}

const defaultMeta = (b: BotEntry): string => {
  const tags = [...(b.triggers ?? []), ...(b.capabilities ?? [])];
  if (tags.length === 0) return "";
  const joined = tags.join(", ");
  return joined.length > 32 ? joined.slice(0, 29) + "…" : joined;
};

/** BotPicker is the Board ticket form's bot selector. Empty value =
 *  "use dispatcher default" (the per-assignee / global workflow from
 *  the dispatcher config). A non-empty value overrides that choice at
 *  launch time (pkg/dispatcher/loop.go buildSpec).
 */
export function BotPicker({
  value,
  bots,
  onChange,
  disabled,
  metaFor = defaultMeta,
}: Props) {
  const options = useMemo<ComboboxOption<string>[]>(() => {
    // Disabled bots (catalog toggle off) aren't routable, so they don't
    // belong in the ticket assignment picker. The current value is kept
    // even if disabled so an existing assignment still renders.
    // A single .bot file is not routable for a card: the dispatcher resolves
    // a ticket's bot by catalog name (botregistry.ResolveBotPath), so files
    // are never offered here — they appear in Dispatcher settings instead.
    const visible = bots.filter(
      (b) => b.origin !== "file" && (b.enabled !== false || botPickerValue(b) === value),
    );
    // Grouped by where the bot comes from: project bundles, team/platform,
    // then single .bot files ("Project files"). The
    // origin label rides in the meta column so each row says where it is
    // from; the Combobox itself has no group headers.
    return groupBotsBySource(visible).flatMap((g) =>
      g.bots.map((b) => {
        // The manifest persona (display_name) leads, with the emoji
        // avatar; the technical name moves into the description and stays
        // in the search haystack so operators can still type "feature_dev".
        const persona = b.display_name?.trim();
        const { emoji } = botIdentity(b.name);
        const label = persona ? `${emoji} ${persona}` : b.name;
        const description = persona
          ? `${b.name}${b.description ? ` — ${b.description}` : ""}`
          : b.origin === "file"
            ? b.rel_path || b.path
            : b.description;
        const source = botSource(b);
        const meta = [source === "project" ? "" : BOT_SOURCE_LABEL[source], metaFor(b)]
          .filter(Boolean)
          .join(" · ");
        return {
          value: botPickerValue(b),
          label,
          description,
          meta: meta || undefined,
          searchHaystack: `${persona ?? ""} ${b.name} ${b.description ?? ""} ${b.rel_path ?? ""} ${BOT_SOURCE_LABEL[source]} ${(b.triggers ?? []).join(" ")} ${(b.capabilities ?? []).join(" ")}`,
        };
      }),
    );
  }, [bots, metaFor, value]);

  return (
    <Combobox<string>
      value={value}
      options={options}
      onChange={(v) => onChange(v)}
      emptyLabel="(dispatcher default)"
      emptyDescription="Falls back to the workflow configured under Dispatcher settings."
      placeholder="Search bots…"
      disabled={disabled}
    />
  );
}
