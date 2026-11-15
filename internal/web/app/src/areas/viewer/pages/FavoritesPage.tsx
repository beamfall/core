import { AppShell } from "@/components/chrome/AppShell";
import { useT } from "@/lib/i18n";
import { ProfileFeed } from "../components/ProfileFeed";
import { ViewerActions } from "../components/ViewerActions";

// The acting Profile's favorited Media, newest first (CORE-1635).
export function FavoritesPage() {
  const t = useT();
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-6">
        <h1 className="bf-page-title">{t("favoritesTitle")}</h1>
        <ProfileFeed kind="favorites" empty={t("favoritesEmpty")} />
      </div>
    </AppShell>
  );
}
