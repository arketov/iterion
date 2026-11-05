// Which registry pages THIS server answers — the sidebar's gates, as data, so
// the Sitemap page can list only what is reachable in the current mode. Kept
// next to NavLinks.tsx (which gates the rail inline) and mirrored from it: a
// page the rail hides for lack of a server_info flag is hidden here too.

import type { ServerInfo } from "@/api/types";

/** Whether the studio page at `href` is available on a server described by
 *  `info`. `info` is null while server_info is still loading: only the pages
 *  every mode answers are listed then. */
export function isNavHrefAvailable(
  href: string,
  info: ServerInfo | null,
): boolean {
  const cloud = info?.mode === "cloud";
  switch (href) {
    case "/editor":
      // Local always has the editor; cloud only with the team bot store.
      return !cloud || !!info?.bot_editing_enabled;
    case "/board":
    case "/pipelines":
      return !!info?.native_tracker_enabled;
    case "/dispatcher":
      return !!info?.dispatcher_enabled;
    case "/triggers":
      return !!info && (!!info.triggers_enabled || cloud);
    case "/marketplace":
      return !!info?.marketplace_enabled;
    case "/plugins":
      return !!info?.plugins_enabled;
    case "/skills":
      return !!info?.skills_enabled;
    case "/config-editor":
      return !!info?.config_shares_enabled;
    case "/secrets":
      return !!info?.secrets_enabled;
    case "/integrations":
      return cloud;
    default:
      return true;
  }
}
