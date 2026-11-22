import { useEffect, useState } from "react";
import { fetchMedia } from "@/areas/viewer/lib/mediaDetail";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { fixMatchCurrent, fixMatchSources } from "@/lib/graphql";
import type { FixMatchSourceInfo } from "@/lib/graphql/manage";
import { useT } from "@/lib/i18n";
import { clearViewerCache } from "@/lib/viewerCache";
import { type AssignItem, AssignPanel } from "./AssignPanel";

type Selection = Pick<AssignItem, "mediaID"> & Partial<AssignItem>;

interface Assignment {
  items: [AssignItem, ...AssignItem[]];
  sources: FixMatchSourceInfo[];
  allSources: FixMatchSourceInfo[];
}

async function loadAssignment(
  selection: readonly [Selection, ...Selection[]],
  signal: AbortSignal,
): Promise<Assignment> {
  const load = async (item: Selection) => {
    const [media, current, sources] = await Promise.all([
      item.title !== undefined ? null : fetchMedia(item.mediaID, signal),
      fixMatchCurrent(item.mediaID),
      fixMatchSources(item.mediaID),
    ]);
    if (!media && item.title === undefined) throw new Error("Media unavailable");
    return {
      item: {
        mediaID: item.mediaID,
        title: media?.title ?? item.title ?? "",
        year: media?.year ?? item.year,
        sequence: media?.sequence ?? item.sequence,
        index: media?.index ?? item.index,
        current,
      },
      sources,
    };
  };
  const [first, ...rest] = await Promise.all(selection.map(load));
  const sameSource = (a: FixMatchSourceInfo, b: FixMatchSourceInfo) =>
    a.pluginID === b.pluginID && a.scraperID === b.scraperID;
  return {
    items: [first.item, ...rest.map(({ item }) => item)],
    sources: first.sources.filter((source) =>
      rest.every((entry) => entry.sources.some((other) => sameSource(source, other))),
    ),
    allSources: [first, ...rest].flatMap(({ sources }) => sources),
  };
}

/** The caller supplies a selection from the manager-only entry point. */
export function AssignDialog({
  selection,
  disabled,
  onChanged,
}: {
  selection: readonly Selection[];
  disabled?: boolean;
  onChanged?: () => void;
}) {
  const t = useT();
  const [opened, setOpened] = useState<readonly [Selection, ...Selection[]] | null>(null);
  const [assignment, setAssignment] = useState<Assignment | null>(null);
  const [failed, setFailed] = useState(false);
  const [writing, setWriting] = useState(false);
  const [changed, setChanged] = useState(false);

  useEffect(() => {
    if (!opened) return;
    const controller = new AbortController();
    loadAssignment(opened, controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setAssignment(next);
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => controller.abort();
  }, [opened]);

  return (
    <Dialog
      open={opened !== null}
      onOpenChange={(open) => {
        if (writing) return;
        if (open) {
          const [first, ...rest] = new Map(selection.map((item) => [item.mediaID, item])).values();
          if (first) setOpened([first, ...rest]);
        } else {
          setOpened(null);
          setAssignment(null);
          setFailed(false);
          if (changed) onChanged?.();
          setChanged(false);
        }
      }}
    >
      <DialogTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled || selection.length === 0}
        >
          {t("assignAction")}
        </Button>
      </DialogTrigger>
      <DialogContent
        className="max-h-[85dvh] overflow-y-auto sm:max-w-xl"
        showCloseButton={!writing}
      >
        <DialogTitle>{t("assignHeading")}</DialogTitle>
        {failed && <p role="alert">{t("assignLoadFailed")}</p>}
        {!failed && !assignment && <p role="status">{t("loading")}</p>}
        {assignment && (
          <AssignPanel
            items={assignment.items}
            sources={assignment.sources}
            matchSources={assignment.allSources}
            defaultSource={assignment.sources.find((source) => source.isDefault)}
            dialog
            onWriteChange={setWriting}
            onFinished={(results) => {
              if (results.some((result) => !result.error)) {
                clearViewerCache();
                setChanged(true);
              }
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
