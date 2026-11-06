// Project registry HTTP client. Mirrors pkg/server/projects.go.
//
// The same on-disk file is shared with the desktop (Wails) app. The
// frontend uses this client in browser/server mode; in desktop mode
// `useProjects` delegates to the Wails bridge (useDesktop.ts) so both
// surfaces stay consistent.

import { request } from "./client";

export interface Project {
  id: string;
  name: string;
  // kind discriminates a local project ("local"/absent) from a remote cloud
  // connection ("cloud"). Only the desktop bridge populates cloud entries;
  // the server projects endpoint always returns local ones.
  kind?: "local" | "cloud";
  dir: string;
  store_dir?: string;
  state?: "starting" | "ready" | "degraded";
  runtime_ready?: boolean;
  error?: string;
  scoped_url?: string;
  last_opened: string; // ISO 8601 UTC
  color?: string;
  // Cloud-connection fields (kind === "cloud" only).
  cloud_url?: string;
  cloud_email?: string;
}

export function listProjects(): Promise<Project[]> {
  return request<Project[]>("/projects");
}

export function getCurrentProject(): Promise<Project | null> {
  return request<Project | null>("/projects/current");
}

export function switchProject(id: string): Promise<Project> {
  return request<Project>("/projects/switch", {
    method: "POST",
    body: JSON.stringify({ id }),
  });
}

export function addProject(dir: string): Promise<Project> {
  return request<Project>("/projects", {
    method: "POST",
    body: JSON.stringify({ dir }),
  });
}

export function removeProject(id: string): Promise<void> {
  return request<void>(`/projects/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export interface FilesystemEntry {
  name: string;
  abs_dir: string;
}

export interface FilesystemListing {
  cwd: string;
  root: string;
  entries: FilesystemEntry[];
}

export function listFilesystem(
  path: string,
  signal?: AbortSignal,
): Promise<FilesystemListing> {
  const qs = `?path=${encodeURIComponent(path)}`;
  return request<FilesystemListing>(`/filesystem/list${qs}`, { signal });
}

// A registered project whose folder is gone but whose store still holds data:
// the candidate for "move the data of a moved project". Mirrors
// pkg/server/projects/relocate.go.
export interface RelocationSource {
  id: string;
  name: string;
  dir: string;
  store_dir: string;
  cards: number;
  runs: number;
  has_dispatcher_config: boolean;
}

export interface RelocationOffer {
  sources: RelocationSource[];
  // False once the project's own store holds a board, settings or runs: the
  // server refuses to merge into it.
  destination_empty: boolean;
}

export function getRelocationOffer(id: string): Promise<RelocationOffer> {
  return request<RelocationOffer>(
    `/projects/${encodeURIComponent(id)}/relocation`,
  );
}

export function relocateProject(
  id: string,
  sourceId: string,
): Promise<{ copied: { cards: number; runs: number }; dispatcher: "stopped" }> {
  return request(`/projects/${encodeURIComponent(id)}/relocate`, {
    method: "POST",
    body: JSON.stringify({ source_id: sourceId }),
  });
}
