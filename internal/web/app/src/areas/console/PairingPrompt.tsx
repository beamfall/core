import { MonitorSmartphone } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { usePairingRequests, useRoleLabel } from "./lib/pairingRequests";

// A device asking to join, raised on any Console screen while an Admin Session
// is open (CORE-2064). It shows the oldest waiting request with the code to
// match against the device and one Approve button; Deny is the quieter
// choice. It is not modal and does not take focus from a field the admin is
// typing in: focus moves to Approve only when nothing has focus. The device
// name and platform are chosen by the device, so they render as text only.
export function PairingPrompt() {
  const t = useT();
  const roleLabel = useRoleLabel();
  const { requests, decide } = usePairingRequests();
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const approveRef = useRef<HTMLButtonElement>(null);
  const titleID = useId();
  const hintID = useId();
  const current = requests[0];
  const code = current?.code;

  useEffect(() => {
    setFailed(false);
    if (code && (!document.activeElement || document.activeElement === document.body)) {
      approveRef.current?.focus();
    }
  }, [code]);

  if (!current) return null;

  async function onDecide(decision: "approve" | "deny") {
    setBusy(true);
    try {
      setFailed(!(await decide(decision, current.code)));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="console-pairing-prompt console-card"
      role="alertdialog"
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
      {failed && (
        <p role="status" className="m-0 mt-1 text-[12px] text-destructive">
          {t("pairingFailed")}
        </p>
      )}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button ref={approveRef} disabled={busy} onClick={() => void onDecide("approve")}>
          {t("pairingApprove")}
        </Button>
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
    </div>
  );
}
