import { Download, RefreshCw } from "lucide-react";
import { EmptyState, ErrorState } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchServerLogs, type ServerLogsInfo } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The downloaded bundle is a plain-text file for a support thread: a short
// header, then the same redacted lines the page shows. The server redacts
// before a line is kept, so the bundle carries nothing the page does not.
export function serverLogBundle(info: ServerLogsInfo, generatedAt: Date): string {
  const header = [
    "# Beamfall server log bundle",
    `# Generated: ${generatedAt.toISOString()}`,
    `# Lines: ${info.lines.length} (server keeps up to ${info.capacity})`,
    "# Redacted on the server: tokens, secrets, IP addresses, home folder user names",
    "",
  ];
  return `${[...header, ...info.lines].join("\n")}\n`;
}

function bundleFileName(generatedAt: Date): string {
  return `beamfall-server-log-${generatedAt.toISOString().replace(/[:.]/g, "-")}.txt`;
}

// Server logs (CORE-1716): the most recent redacted lines of the operator
// log, read through the Admin-Session gated serverLogs field, with a
// downloadable bundle. Read-only.
export function ServerLogs() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync((signal) =>
    fetchServerLogs(undefined, signal),
  );

  const onDownload = () => {
    if (!data) return;
    const generatedAt = new Date();
    const blob = new Blob([serverLogBundle(data, generatedAt)], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = bundleFileName(generatedAt);
    a.click();
    URL.revokeObjectURL(url);
  };

  const hasLines = !!data?.available && data.lines.length > 0;

  return (
    <Section
      heading="serverLogsHeading"
      lead="serverLogsLead"
      actions={
        <>
          <Button variant="outline" size="sm" onClick={() => void refresh()}>
            <RefreshCw className="console-nav-icon" aria-hidden />
            {t("refresh")}
          </Button>
          <Button variant="outline" size="sm" disabled={!hasLines} onClick={onDownload}>
            <Download className="console-nav-icon" aria-hidden />
            {t("serverLogsDownload")}
          </Button>
        </>
      }
    >
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed || !data ? (
        <ErrorState message="serverLogsUnavailable" />
      ) : !data.available ? (
        <EmptyState message="serverLogsNotKept" />
      ) : data.lines.length === 0 ? (
        <EmptyState message="serverLogsEmpty" />
      ) : (
        <>
          <pre
            aria-label={t("serverLogsLinesLabel")}
            // The log scrolls, so keyboard users need to reach it.
            tabIndex={0}
            className="console-mono max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted p-3 text-xs leading-relaxed"
          >
            {data.lines.join("\n")}
          </pre>
          <p className="text-xs text-muted-foreground" aria-live="polite">
            {t("serverLogsShowing")
              .replace("{count}", String(data.lines.length))
              .replace("{capacity}", String(data.capacity))}
          </p>
        </>
      )}
    </Section>
  );
}
