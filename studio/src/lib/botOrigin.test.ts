import { describe, expect, it } from "vitest";

import type { BotEntryWithSchema } from "@/api/bots";

import {
  botPickerValue,
  botSource,
  botWorkflowPath,
  countBots,
  groupBotsBySource,
  isManifestless,
} from "./botOrigin";

const bot = (name: string, origin?: BotEntryWithSchema["origin"], path = `/p/${name}`): BotEntryWithSchema =>
  ({ name, path, origin }) as BotEntryWithSchema;

describe("botOrigin", () => {
  it("classifies origins", () => {
    expect(botSource(bot("a"))).toBe("project");
    expect(botSource(bot("a", "catalog"))).toBe("project");
    expect(botSource(bot("a", "file"))).toBe("file");
    expect(botSource(bot("a", "tenant"))).toBe("team");
    expect(botSource(bot("a", "platform"))).toBe("platform");
    expect(isManifestless(bot("a", "file"))).toBe(true);
    expect(isManifestless(bot("a", "catalog"))).toBe(false);
  });

  it("stores a path for files, a name otherwise", () => {
    expect(botPickerValue(bot("t", "file", "/w/t.bot"))).toBe("/w/t.bot");
    expect(botPickerValue(bot("x", "catalog"))).toBe("x");
  });

  it("stores a bot's own path as its workflow path", () => {
    expect(botWorkflowPath(bot("x", "catalog", "/w/bots/x"))).toBe("/w/bots/x");
  });

  it("groups project bots first and project files last, dropping empty groups", () => {
    const groups = groupBotsBySource([
      bot("f", "file"),
      bot("p", "catalog"),
    ]);
    expect(groups.map((g) => g.label)).toEqual(["Project", "Project files"]);
    expect(groupBotsBySource([bot("p")]).length).toBe(1);
  });
});

describe("countBots", () => {
  it("counts catalog bots as bots and keeps project files apart", () => {
    const c = countBots([bot("a", "catalog"), bot("b", "catalog")], [
      bot("t", "file"),
      bot("u", "file"),
    ]);
    expect(c).toEqual({ total: 2, catalog: 2, files: 2 });
    expect(countBots(null, [])).toEqual({ total: 0, catalog: 0, files: 0 });
  });
});
