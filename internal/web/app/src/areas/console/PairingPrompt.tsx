import { MonitorSmartphone } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { usePairingRequests, useRoleLabel } from "./lib/pairingRequests";

// A device asking to join, raised on any Console screen while an Admin Session
// is open (CORE-2064). It shows the oldest waiting request with the code to
// match against the device. It is not modal and never takes focus: a key
// press meant for the page must not approve a device, so the admin reaches
// the buttons deliberately. A polite live region, present before any request
// arrives, announces the request to a screen reader instead.
//
// Approve grants Viewer. A device that asked for manager gets a second,
// separately named button: the role a device asks for is never granted by the
// ordinary approval. The device name and platform are chosen by the device,
// so they render as text only.
export function PairingPrompt() {
  const t = useT();
  const roleLabel = useRoleLabel();
  const { requests, decide } = usePairingRequests();
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const titleID = useId();
  const hintID = useId();
  const current = requests[0];
  const code = current?.code;

  // A new request clears the last failure.
  useEffect(() => setFailed(false), [code]);

  async function onDecide(decision: "approve" | "deny", grant?: "manager") {
    if (!current) return;
    setBusy(true);
    try {
      setFailed(!(await decide(decision, current.code, grant)));
    } finally {
      setBusy(false);
    }
  }

  const wantsManager = current?.requestedRole === "manager";
  return (
    <>
      <p role="status" aria-live="polite" className="sr-only">
        {current && t("pairingPromptAnnounce", { name: current.deviceName, code: current.code })}
      </p>
      {current && (
        <section
          className="console-pairing-prompt"
          aria-labelledby={titleID}
          aria-describedby={hintID}
        >
          <p id={titleID} className="console-secret-lead">
            <MonitorSmartphone className="console-nav-icon" aria-hidden />
            {t("pairingPromptTitle")}
          </p>
          <p className="m-0 text-dense font-semibold break-words">{current.deviceName}</p>
          <p className="m-0 text-[12px] leading-snug text-muted-foreground break-words">
            {[
              current.platform,
              t("pairingWantsRole", { role: roleLabel(current.requestedRole) }),
              t(current.lan ? "pairingNetworkLan" : "pairingNetworkRemote"),
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
          <p className="m-0 mt-2 text-[12px] text-muted-foreground">{t("pairingPromptCode")}</p>
          <code className="console-secret-value console-pairing-code">{current.code}</code>
          <p id={hintID} className="m-0 mt-1 text-[12px] leading-snug text-muted-foreground">
            {t("pairingPromptHint")}
          </p>
          {wantsManager && (
            <p className="m-0 mt-1 text-[12px] leading-snug text-muted-foreground">
              {t("pairingManagerWarning")}
            </p>
          )}
          {failed && (
            <p role="status" className="m-0 mt-1 text-[12px] text-destructive">
              {t("pairingFailed")}
            </p>
          )}
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button disabled={busy} onClick={() => void onDecide("approve")}>
              {t(wantsManager ? "pairingApproveViewer" : "pairingApprove")}
            </Button>
            {wantsManager && (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => void onDecide("approve", "manager")}
              >
                {t("pairingApproveManager")}
              </Button>
            )}
            <Button variant="ghost" disabled={busy} onClick={() => void onDecide("deny")}>
              {t("pairingDeny")}
            </Button>
            {requests.length > 1 && (
              <Link
                to="/console/access"
                className="ml-auto text-[12px] text-muted-foreground underline"
              >
                {t("pairingMoreWaiting", { count: requests.length - 1 })}
              </Link>
            )}
          </div>
        </section>
      )}
    </>
  );
}
