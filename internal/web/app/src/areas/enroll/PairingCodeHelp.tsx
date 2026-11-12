import { useId } from "react";
import { useT } from "@/lib/i18n";

// Plain-words answer to "where do I get a pairing code?" for someone who has
// never seen this server (CORE-1196). Kept apart from the form so the sign-in
// page can change its layout without touching the guidance.
export function PairingCodeHelp() {
  const t = useT();
  const titleID = useId();
  return (
    <section aria-labelledby={titleID} className="mt-10 border-t pt-6">
      <h2 id={titleID} className="text-lg font-semibold">
        {t("signInWhereTitle")}
      </h2>
      <ul className="mt-3 flex list-disc flex-col gap-3 pl-5 text-sm leading-relaxed text-muted-foreground">
        <li>{t("signInWhereDevice")}</li>
        <li>
          {t("signInWhereServer")}{" "}
          <code className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-[13px] text-foreground">
            beamfall pairing-code
          </code>
        </li>
        <li>{t("signInWhereAsk")}</li>
      </ul>
    </section>
  );
}
