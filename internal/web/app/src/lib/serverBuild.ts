import { useEffect, useState } from "react";
import type { useT } from "@/lib/i18n";

interface ServerBuild {
  version: string;
  commit: string | null;
  buildDate: string | null;
}

let cachedBuild: ServerBuild | null = null;
let buildRequest: Promise<ServerBuild | null> | undefined;

// The server serves the bundle with every "/web" literal rewritten under its
// configured prefix (rewriteWebBasePaths), so the app base below becomes, for
// example, "/media/web/" and health resolves beside it as "/media/health".
const WEB_BASE = "/web/";

export function healthUrl(webBase: string = WEB_BASE): string {
  return webBase.replace(/\/web\/?$/, "") + "/health";
}

async function fetchServerBuild(): Promise<ServerBuild | null> {
  try {
    const response = await fetch(healthUrl(), { credentials: "same-origin", cache: "no-store" });
    // Readiness does not affect the identity of the running build.
    if (!response.ok && response.status !== 503) return null;
    const report: { version?: unknown; commit?: unknown; buildDate?: unknown } | null =
      await response.json();
    if (typeof report?.version !== "string" || !report.version) return null;
    return {
      version: report.version,
      commit:
        typeof report.commit === "string" && report.commit && report.commit !== "development"
          ? report.commit
          : null,
      buildDate:
        typeof report.buildDate === "string" && Number.isFinite(Date.parse(report.buildDate))
          ? report.buildDate
          : null,
    };
  } catch {
    return null;
  }
}

export function useServerBuild(): ServerBuild | null {
  const [build, setBuild] = useState(cachedBuild);
  useEffect(() => {
    // Routes remount the shared shell. Keep the in-flight request and its
    // result for the life of the page.
    buildRequest ??= fetchServerBuild().then((result) => {
      cachedBuild = result;
      return result;
    });
    let active = true;
    void buildRequest.then((result) => {
      if (active) setBuild(result);
    });
    return () => {
      active = false;
    };
  }, []);
  return build;
}

export function formatServerBuild(build: ServerBuild, locale: string, t: ReturnType<typeof useT>) {
  const { version, commit, buildDate } = build;
  const development = version === "development" || version.startsWith("development+");
  let shortVersion = version;
  if (development) {
    shortVersion = commit
      ? t("serverBuildDevelopmentCommit", { commit: commit.slice(0, 7) })
      : t("serverBuildDevelopment");
  }

  const details = [t("serverBuildVersionDetail", { version })];
  let label = shortVersion;
  if (buildDate) {
    const date = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeZone: "UTC" }).format(
      new Date(buildDate),
    );
    label = t("serverBuildWithDate", { version: shortVersion, date });
    details.push(t("serverBuildDateDetail", { date: buildDate }));
  }
  if (commit) details.push(t("serverBuildCommitDetail", { commit }));

  return { label, details: new Intl.ListFormat(locale).format(details) };
}
