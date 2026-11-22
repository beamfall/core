import { ApiError, CSRF_HEADER, postJSON, profileHeaders } from "@/lib/api";

// Media upload client (CORE-3193). Files go up in chunks to the admin-only
// /api/media/uploads routes; each chunk names the byte offset it starts at, so
// a dropped chunk resumes from the offset the server reports instead of
// starting over. The session id is remembered per file for this tab, so a
// reload resumes a half-sent file too.

const UPLOADS = "/api/media/uploads";
const RESUME_KEY = "beamfall.mediaUpload.";
const CHUNK_RETRIES = 3;

export type MediaUploadStatus = {
  uploadID: string;
  fileName: string;
  relativeDir: string;
  size: number;
  offset: number;
  chunkSize: number;
};

export type MediaUploadParse = {
  title: string;
  year?: number;
  sequence?: string;
  index?: number;
  indexEnd?: number;
};

export type MediaUploadTarget = {
  entityID: string;
  title: string;
  year?: number;
  kindLabel: string;
  destination: string;
};

export type MediaUploadProposal = {
  uploadID: string;
  fileName: string;
  parsed?: MediaUploadParse;
  target?: MediaUploadTarget;
  destination: string;
  rescan?: MediaUploadParse;
  conflict: boolean;
};

export type MediaUploadConflict = "keep" | "replace" | "fail";

export type MediaUploadCommitItem = {
  uploadID: string;
  destination: string;
  conflict: MediaUploadConflict;
};

export type MediaUploadCommitResult = {
  uploadID: string;
  path?: string;
  trashedPath?: string;
  trashID?: string;
  error?: string;
};

function uploadURL(id: string): string {
  return `${UPLOADS}/${encodeURIComponent(id)}`;
}

async function sendUpload(
  method: "GET" | "PUT" | "DELETE",
  id: string,
  init: { body?: Blob; offset?: number; signal?: AbortSignal } = {},
): Promise<Response> {
  const headers: Record<string, string> = { ...profileHeaders(), [CSRF_HEADER]: "1" };
  if (init.offset !== undefined) {
    headers["Upload-Offset"] = String(init.offset);
    headers["Content-Type"] = "application/octet-stream";
  }
  return fetch(uploadURL(id), {
    method,
    credentials: "same-origin",
    headers,
    body: init.body,
    signal: init.signal,
  });
}

async function readStatus(response: Response): Promise<MediaUploadStatus> {
  return (await response.json()) as MediaUploadStatus;
}

/** The tab-local resume key for a file: name, size and modification time. */
export function resumeKey(libraryID: string, file: File, relativeDir: string): string {
  return `${RESUME_KEY}${libraryID}:${relativeDir}/${file.name}:${file.size}:${file.lastModified}`;
}

function rememberedUpload(key: string): string | null {
  try {
    return sessionStorage.getItem(key);
  } catch {
    return null;
  }
}

function rememberUpload(key: string, id: string | null): void {
  try {
    if (id) sessionStorage.setItem(key, id);
    else sessionStorage.removeItem(key);
  } catch {
    // Resume across reloads is a convenience; the upload still works.
  }
}

/** Opens (or reopens, after a reload) the staged upload for one file. */
export async function openMediaUpload(
  libraryID: string,
  file: File,
  relativeDir: string,
  signal?: AbortSignal,
): Promise<MediaUploadStatus> {
  const key = resumeKey(libraryID, file, relativeDir);
  const remembered = rememberedUpload(key);
  if (remembered) {
    const response = await sendUpload("GET", remembered, { signal });
    if (response.ok) {
      const status = await readStatus(response);
      if (status.size === file.size) return status;
    }
    rememberUpload(key, null);
  }
  const status = await postJSON<MediaUploadStatus>(
    UPLOADS,
    { libraryID, fileName: file.name, relativeDir, size: file.size },
    { signal },
  );
  rememberUpload(key, status.uploadID);
  return status;
}

/**
 * Sends the rest of a file from the server's offset. A failed or refused
 * chunk re-reads the offset and resumes from there, up to CHUNK_RETRIES
 * times in a row.
 */
export async function sendMediaUpload(
  file: File,
  start: MediaUploadStatus,
  onProgress: (sent: number) => void,
  signal?: AbortSignal,
): Promise<MediaUploadStatus> {
  let status = start;
  let failures = 0;
  const chunkSize = Math.max(1, status.chunkSize);
  onProgress(status.offset);
  while (status.offset < status.size) {
    const end = Math.min(status.size, status.offset + chunkSize);
    try {
      const response = await sendUpload("PUT", status.uploadID, {
        body: file.slice(status.offset, end),
        offset: status.offset,
        signal,
      });
      if (response.ok || response.status === 409) {
        status = await readStatus(response);
        if (response.ok) failures = 0;
        else failures += 1;
      } else if (response.status < 500) {
        throw new ApiError(`PUT ${UPLOADS} failed`, response.status);
      } else {
        failures += 1;
        status = await currentStatus(status.uploadID, signal);
      }
    } catch (err) {
      if (signal?.aborted || err instanceof ApiError) throw err;
      failures += 1;
      if (failures > CHUNK_RETRIES) throw err;
      status = await currentStatus(status.uploadID, signal);
    }
    if (failures > CHUNK_RETRIES) throw new Error("upload stalled");
    onProgress(status.offset);
  }
  return status;
}

async function currentStatus(id: string, signal?: AbortSignal): Promise<MediaUploadStatus> {
  const response = await sendUpload("GET", id, { signal });
  if (!response.ok) throw new ApiError(`GET ${UPLOADS} failed`, response.status);
  return readStatus(response);
}

export async function cancelMediaUpload(
  libraryID: string,
  file: File,
  relativeDir: string,
  id: string,
): Promise<void> {
  rememberUpload(resumeKey(libraryID, file, relativeDir), null);
  await sendUpload("DELETE", id);
}

export function forgetMediaUpload(libraryID: string, file: File, relativeDir: string): void {
  rememberUpload(resumeKey(libraryID, file, relativeDir), null);
}

export async function proposeMediaUploads(
  libraryID: string,
  uploadIDs: string[],
): Promise<MediaUploadProposal[]> {
  const result = await postJSON<{ proposals: MediaUploadProposal[] }>(`${UPLOADS}/proposals`, {
    libraryID,
    uploadIDs,
  });
  return result.proposals ?? [];
}

export async function searchMediaUploadTargets(
  libraryID: string,
  query: string,
  signal?: AbortSignal,
): Promise<MediaUploadTarget[]> {
  const result = await postJSON<{ targets: MediaUploadTarget[] }>(
    `${UPLOADS}/targets`,
    { libraryID, query },
    { signal },
  );
  return result.targets ?? [];
}

export async function commitMediaUploads(
  items: MediaUploadCommitItem[],
): Promise<MediaUploadCommitResult[]> {
  const result = await postJSON<{ results: MediaUploadCommitResult[] }>(`${UPLOADS}/commit`, {
    items,
  });
  return result.results ?? [];
}

/** The folder a dropped file came from, relative to the drop, if any. */
export function droppedRelativeDir(file: File): string {
  const path = (file as File & { webkitRelativePath?: string }).webkitRelativePath ?? "";
  const slash = path.lastIndexOf("/");
  return slash > 0 ? path.slice(0, slash) : "";
}
