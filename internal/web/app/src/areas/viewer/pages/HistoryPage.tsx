import { AppShell } from "@/components/chrome/AppShell";
import { useT } from "@/lib/i18n";
import { ProfileFeed } from "../components/ProfileFeed";
import { ViewerActions } from "../components/ViewerActions";

// The acting Profile's play history: recently played, then most played (CORE-1635).
export function HistoryPage() {
  const t = useT();
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-8">
        <h1 className="bf-page-title">{t("historyTitle")}</h1>
        <ProfileFeed
          kind="recently-played"
          heading={t("historyRecent")}
          empty={t("historyEmpty")}
        />
        <ProfileFeed kind="most-played" heading={t("historyMost")} empty={t("historyEmpty")} />
      </div>
    </AppShell>
  );
}
