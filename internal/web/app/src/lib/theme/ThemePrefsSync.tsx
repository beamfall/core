import { useEffect } from "react";
import { fetchClientPrefs } from "@/lib/graphql/viewer";
import { useRegister } from "@/lib/register";
import { useSession } from "@/lib/session";
import { isPresetId, THEME_PREF_KEYS } from "./runtime";
import { THEME_DEFAULT_PRESET } from "./theme.generated";

/**
 * Applies the signed-in Profile's theme preset (theme.preset in profile_client_prefs) once per
 * Profile (CORE-1179). A Profile without a stored preset gets the default. Failures keep the
 * theme already on screen, which is the device's cached copy.
 */
export function ThemePrefsSync() {
  const { session } = useSession();
  const { setPreset } = useRegister();
  const profileID = session?.profileID ?? null;

  useEffect(() => {
    if (!profileID) return;
    let cancelled = false;
    fetchClientPrefs()
      .then((prefs) => {
        if (cancelled) return;
        const stored = prefs.find((pref) => pref.key === THEME_PREF_KEYS.preset)?.value;
        setPreset(isPresetId(stored) ? stored : THEME_DEFAULT_PRESET);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [profileID, setPreset]);

  return null;
}
