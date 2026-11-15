import { isHttpsURL } from "@/components/beamfall/MetadataAttributionText";
import { displayString } from "@/lib/display";
import { useDisplayLocale, useT } from "@/lib/i18n";
import type { ProviderKey } from "@/lib/providerKeys";

/**
 * The field label for a provider key: the plugin's own credential name when it
 * declares one (CORE-1127, for example a subscriber PIN), else "<provider> API key".
 */
export function useProviderKeyFieldLabel(): (key: ProviderKey) => string {
  const t = useT();
  const locale = useDisplayLocale();
  return (key) =>
    displayString(key.credentialHelp?.label, locale) ||
    t("pluginKeyField", { provider: key.label });
}

// CredentialHelpLink (CORE-1127): the plugin-declared page where an owner gets
// the credential. The text is plugin data rendered as text (ADR-0012 §5); only
// an https link renders, opening without an opener or referrer.
export function CredentialHelpLink({ providerKey }: { providerKey: ProviderKey }) {
  const locale = useDisplayLocale();
  const help = providerKey.credentialHelp;
  const label = displayString(help?.helpLabel, locale);
  if (!help?.helpURL || !label || !isHttpsURL(help.helpURL)) return null;
  return (
    <a
      className="block text-xs underline underline-offset-2"
      href={help.helpURL}
      target="_blank"
      rel="noopener noreferrer"
    >
      {label}
    </a>
  );
}
