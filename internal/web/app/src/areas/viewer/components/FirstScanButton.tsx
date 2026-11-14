import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { RescanStatus, startRescan } from "@/components/beamfall/LibraryRescan";
import {
  libraryRootProblems,
  ROOT_PROBLEM_LABEL,
  type RootProblem,
} from "@/components/beamfall/LibraryRootHealth";
import { Button, buttonVariants } from "@/components/ui/button";
import { fetchLibraries } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Home's "Start first scan" (CORE-1606). It scans the Libraries that already
// exist and shows the progress in place; a folder the server cannot find or
// read is named with a link to fix it instead. With no Library (or no folder)
// to scan yet, it opens Console › Libraries as before.
export function FirstScanButton() {
  const t = useT();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [started, setStarted] = useState<string[]>([]);
  const [blocked, setBlocked] = useState<RootProblem[]>([]);

  async function start() {
    setBusy(true);
    try {
      const libraries = (await fetchLibraries()).filter(
        (library) => (library.roots ?? []).length > 0,
      );
      if (libraries.length === 0) {
        navigate("/console/libraries");
        return;
      }
      const problems = await libraryRootProblems(libraries);
      const broken = new Set(problems.map((problem) => problem.libraryID));
      const scannable = libraries.filter((library) => !broken.has(library.id));
      setBlocked(problems);
      setStarted(scannable.map((library) => library.id));
      const outcomes = await Promise.all(
        scannable.map((library) => startRescan(library.id, library.roots ?? [])),
      );
      // A scan needs an Admin Session. When the server refuses for want of one,
      // Console > Libraries asks for the admin password and offers the scan.
      if (outcomes.includes("refused")) navigate("/console/libraries");
    } catch {
      navigate("/console/libraries");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col items-center gap-2">
      <Button type="button" disabled={busy} onClick={() => void start()}>
        {t("libraryEmptyStartScan")}
      </Button>
      {started.map((id) => (
        <RescanStatus key={id} libraryID={id} hasRoots />
      ))}
      {blocked.length > 0 && (
        <div role="alert" className="flex flex-col items-center gap-2 text-sm">
          {blocked.map((problem) => (
            <p key={`${problem.libraryID}:${problem.root}`} className="m-0 break-all">
              {t("firstScanBlocked", {
                path: problem.root,
                reason: t(ROOT_PROBLEM_LABEL[problem.reason]).toLowerCase(),
              })}
            </p>
          ))}
          <Link
            to="/console/libraries"
            className={buttonVariants({ variant: "outline", size: "sm" })}
          >
            {t("firstScanFixFolder")}
          </Link>
        </div>
      )}
    </div>
  );
}
