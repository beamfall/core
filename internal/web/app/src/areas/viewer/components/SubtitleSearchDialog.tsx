import { type FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import {
  downloadSubtitle,
  type SubtitleSearchResult,
  searchSubtitles,
} from "../lib/subtitleSearch";
import { languageName } from "../player/streamTracks";

// "Search online…" for subtitles (CORE-1690), from the Media page's More menu
// and the player's subtitle menu. The server matches by the file's hash and
// the item's generic metadata; a download is written beside the media file as
// a sidecar, so onDownloaded refreshes the caller's track list.

function isNoKey(error: unknown): boolean {
  if (!(error instanceof ApiError) || error.status !== 409) return false;
  const payload = error.payload as { code?: unknown } | undefined;
  return payload?.code === "NO_KEY";
}

function defaultLanguages(): string {
  const tag = typeof navigator === "undefined" ? "" : (navigator.language ?? "");
  return tag.split("-")[0]?.toLowerCase() || "en";
}

export function SubtitleSearchDialog({
  mediaID,
  title,
  onClose,
  onDownloaded,
}: {
  mediaID: string;
  title: string;
  onClose: () => void;
  onDownloaded?: () => void;
}) {
  const { t, locale } = useI18n();
  const [languages, setLanguages] = useState(defaultLanguages);
  const [results, setResults] = useState<SubtitleSearchResult[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [downloading, setDownloading] = useState("");
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");

  async function search(event: FormEvent) {
    event.preventDefault();
    setSearching(true);
    setError("");
    setStatus("");
    try {
      const list = languages
        .split(",")
        .map((tag) => tag.trim())
        .filter(Boolean);
      setResults(await searchSubtitles(mediaID, list));
    } catch (err) {
      setResults(null);
      setError(t(isNoKey(err) ? "subtitleSearchNoKey" : "subtitleSearchFailed"));
    } finally {
      setSearching(false);
    }
  }

  async function download(result: SubtitleSearchResult) {
    const key = `${result.provider}:${result.fileId}`;
    setDownloading(key);
    setError("");
    setStatus("");
    try {
      const saved = await downloadSubtitle(mediaID, result);
      setStatus(
        t("subtitleSearchDownloaded", {
          language: languageName(saved.language, locale) || saved.language,
        }),
      );
      onDownloaded?.();
    } catch (err) {
      if (isNoKey(err)) setError(t("subtitleSearchNoKey"));
      else if (err instanceof ApiError && err.status === 409) setError(t("subtitleSearchReadOnly"));
      else setError(t("subtitleSearchDownloadFailed"));
    } finally {
      setDownloading("");
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("subtitleSearchTitle", { title })}</DialogTitle>
          <DialogDescription>{t("subtitleSearchDescription")}</DialogDescription>
        </DialogHeader>
        <form className="flex items-end gap-2" onSubmit={(event) => void search(event)}>
          <label className="flex flex-1 flex-col gap-1 text-sm">
            {t("subtitleSearchLanguages")}
            <Input
              value={languages}
              onChange={(event) => setLanguages(event.target.value)}
              aria-describedby="subtitle-search-languages-hint"
            />
            <span id="subtitle-search-languages-hint" className="text-xs text-muted-foreground">
              {t("subtitleSearchLanguagesHint")}
            </span>
          </label>
          <Button type="submit" disabled={searching}>
            {t(searching ? "subtitleSearchSearching" : "subtitleSearchSubmit")}
          </Button>
        </form>
        {error && (
          <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
            {error}
          </p>
        )}
        {results && results.length === 0 && (
          <p className="m-0 text-sm text-muted-foreground">{t("subtitleSearchNoResults")}</p>
        )}
        {results && results.length > 0 && (
          <ul
            className="m-0 flex max-h-80 list-none flex-col gap-1 overflow-y-auto p-0"
            aria-label={t("subtitleSearchTitle", { title })}
          >
            {results.map((result) => {
              const key = `${result.provider}:${result.fileId}`;
              return (
                <li key={key} className="flex items-center justify-between gap-3 py-1">
                  <div className="flex min-w-0 flex-col text-sm">
                    <span className="font-medium">
                      {languageName(result.language, locale) || result.language}
                      {result.forced ? ` · ${t("trackRoleForced")}` : ""}
                      {result.sdh ? ` · ${t("trackRoleSDH")}` : ""}
                    </span>
                    {result.release && (
                      <span className="truncate text-muted-foreground">{result.release}</span>
                    )}
                    <span className="text-xs text-muted-foreground">
                      {[
                        result.hashMatch ? t("subtitleSearchHashMatch") : "",
                        result.rating > 0
                          ? t("subtitleSearchRating", { rating: result.rating.toFixed(1) })
                          : "",
                        t("subtitleSearchSource", { source: result.provider }),
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={downloading !== ""}
                    onClick={() => void download(result)}
                  >
                    {t(
                      downloading === key ? "subtitleSearchDownloading" : "subtitleSearchDownload",
                    )}
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
        <p role="status" className="m-0 min-h-5 text-sm">
          {status}
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("subtitleSearchClose")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
