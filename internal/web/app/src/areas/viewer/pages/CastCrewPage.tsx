import { ArrowLeft } from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Skeleton } from "@/components/ui/skeleton";
import { isAuthRejection } from "@/lib/api";
import type { GenericMediaInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { readViewerCache } from "@/lib/viewerCache";
import { CastRow } from "../components/CastRow";
import { NotAvailable } from "../components/NotAvailable";
import { ViewerActions } from "../components/ViewerActions";
import { mediaIdFromAddress } from "../lib/mediaAddress";
import { fetchMedia, type MediaDetail, mediaDetailCacheKey } from "../lib/mediaDetail";
import "./media-detail.css";

type Status = "loading" | "ready" | "missing" | "failed";

/** The Cast & Crew address of a Media item, the detail page's "See all". */
export const castCrewPath = (mediaId: string) => `/credits/${encodeURIComponent(mediaId)}`;

// Cast & Crew (/credits/:mediaId, CORE-2503): every person credit of one Media
// item, the cast first and then each department, in billing order. It paints
// from the detail page's cached read for this identity and revalidates.
export function CastCrewPage() {
  const mediaId = mediaIdFromAddress(useParams()["*"]);
  const identity = useViewerIdentity();
  const location = useLocation();
  const navigate = useNavigate();
  const t = useT();
  const [media, setMedia] = useState<GenericMediaInfo | null>(
    () => readViewerCache<MediaDetail>(identity, mediaDetailCacheKey(mediaId))?.media ?? null,
  );
  const [status, setStatus] = useState<Status>(media ? "ready" : "loading");

  useEffect(() => {
    if (!mediaId) return;
    let alive = true;
    const controller = new AbortController();
    const cached = readViewerCache<MediaDetail>(identity, mediaDetailCacheKey(mediaId))?.media;
    setMedia(cached ?? null);
    setStatus(cached ? "ready" : "loading");
    fetchMedia(mediaId, controller.signal)
      .then((next) => {
        if (!alive) return;
        setMedia(next);
        setStatus(next ? "ready" : "missing");
      })
      .catch((err: unknown) => {
        if (!alive) return;
        if (isAuthRejection(err)) {
          setMedia(null);
          setStatus("missing");
        } else if (!cached) {
          setStatus("failed");
        }
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [identity, mediaId]);

  function goBack() {
    if (location.key !== "default") navigate(-1);
    else navigate(`/media/${encodeURIComponent(mediaId)}`);
  }

  const current = media?.id === mediaId ? media : null;
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="bf-detail bf-cast-crew">
        <button type="button" className="bf-detail-back" onClick={goBack}>
          <ArrowLeft aria-hidden />
          {t("back")}
        </button>
        {status === "missing" && <NotAvailable />}
        {status === "failed" && !current && (
          <p className="bf-detail-summary" role="alert">
            {t("mediaDetailLoadFailed")}
          </p>
        )}
        {status === "loading" && !current && (
          <Skeleton aria-label={t("loading")} className="h-6 w-48" />
        )}
        {current && status !== "missing" && (
          <>
            <div className="flex flex-col gap-1">
              <p className="bf-detail-summary">{current.title}</p>
              <h1 className="bf-page-title">{t("mediaDetailCastCrew")}</h1>
            </div>
            <CastRow credits={current.credits} castHeading={t("mediaDetailCast")} />
          </>
        )}
      </div>
    </AppShell>
  );
}
