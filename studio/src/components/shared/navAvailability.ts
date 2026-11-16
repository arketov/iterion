// Which registry pages THIS server answers to THIS user — the one place the
// sidebar rail and the Sitemap page both decide whether a page is listed:
// server_info flags plus the caller's access (active team, config-share
// capability). The caller derives canEditConfigShares from
// AuthContext.canEditConfigShares (the mirror of the server gate), so this
// module stays a pure function with no React or auth imports.

import type { ServerInfo } from "@/api/types";

/** What the signed-in user may reach, beyond what the server answers. */
export interface NavAccess {
  hasActiveTeam: boolean;
  canEditConfigShares: boolean;
}

const NO_ACCESS: NavAccess = {
  hasActiveTeam: false,
  canEditConfigShares: false,
};

/** Whether the studio page at `href` is available on a server described by
 *  `info` to a user with `access`. `info` is null while server_info is still
 *  loading: only the pages every mode answers are listed then. */
export function isNavHrefAvailable(
  href: string,
  info: ServerInfo | null,
  access: NavAccess = NO_ACCESS,
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
      return !!info?.config_shares_enabled && access.canEditConfigShares;
    case "/secrets":
      return !!info?.secrets_enabled;
    case "/integrations":
      return cloud && access.hasActiveTeam;
    default:
      return true;
  }
}
