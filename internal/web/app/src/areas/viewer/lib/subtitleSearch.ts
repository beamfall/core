import { useEffect, useState } from "react";
import { getJSON, postJSON } from "@/lib/api";

// Online subtitle search (CORE-1690). The server dials a provider only when
// the operator enabled one; the status call tells the UI whether to offer the
// entry at all, and canSearch whether this Profile and device may use it.

export interface SubtitleSearchStatus {
  enabled: boolean;
  canSearch: boolean;
  providers: { id: string; ready: boolean }[];
}

export interface SubtitleSearchResult {
  provider: string;
  fileId: string;
  language: string;
  release: string;
  rating: number;
  downloads: number;
  hashMatch: boolean;
  sdh: boolean;
  forced: boolean;
}

export function loadSubtitleSearchStatus(): Promise<SubtitleSearchStatus> {
  return getJSON<SubtitleSearchStatus>("/api/media/subtitle-search");
}

export async function searchSubtitles(
  mediaId: string,
  languages: string[],
): Promise<SubtitleSearchResult[]> {
  const body = await postJSON<{ results: SubtitleSearchResult[] | null }>(
    "/api/media/subtitle-search",
    { mediaId, languages },
  );
  return body.results ?? [];
}

export function downloadSubtitle(
  mediaId: string,
  result: SubtitleSearchResult,
): Promise<{ file: string; language: string }> {
  return postJSON("/api/media/subtitle-download", {
    mediaId,
    provider: result.provider,
    fileId: result.fileId,
    language: result.language,
    sdh: result.sdh,
    forced: result.forced,
  });
}

/** True when the server has a provider enabled and this viewer may search.
 *  Nothing is fetched until `enabled` (a menu opened), then once. */
export function useSubtitleSearchAvailable(enabled: boolean): boolean {
  const [available, setAvailable] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    loadSubtitleSearchStatus().then(
      (status) => {
        if (alive) setAvailable(status.enabled && status.canSearch);
      },
      () => {
        if (alive) setAvailable(false);
      },
    );
    return () => {
      alive = false;
    };
  }, [enabled]);
  return available;
}
