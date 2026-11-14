import { useId } from "react";
import { useT } from "@/lib/i18n";

// Plain-words answer to "where do I get a pairing code?" for someone who has
// never seen this server (CORE-1196). Kept apart from the form so the sign-in
// page can change its layout without touching the guidance.
export function PairingCodeHelp() {
  const t = useT();
  const titleID = useId();
  return (
    <section aria-labelledby={titleID}>
      <h2 id={titleID} className="m-0 text-[15px] font-semibold">
        {t("signInWhereTitle")}
      </h2>
      <ul className="m-0 mt-2.5 flex list-disc flex-col gap-2 pl-5 text-sm leading-relaxed text-muted-foreground marker:text-muted-foreground/50">
        <li>
          {t("signInWhereDevice", {
            // The Console's own labels, so the directions match what is on screen.
            page: t("consoleAccessGroup"),
            action: t("deviceAddHeading"),
          })}
        </li>
        <li>{t("signInWhereAsk")}</li>
      </ul>
    </section>
  );
}
