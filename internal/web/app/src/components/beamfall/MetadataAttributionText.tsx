import { displayString } from "@/lib/display";
import type { MetadataAttribution } from "@/lib/graphql";
import { useDisplayLocale } from "@/lib/i18n";

/** True only for an absolute https: URL, the one scheme rendered as a link. */
export function isHttpsURL(url: string): boolean {
  try {
    return new URL(url).protocol === "https:";
  } catch {
    return false;
  }
}

// A plugin-declared metadata credit. The text and link are plugin data rendered
// as text (ADR-0012 §5); an https link opens in a new browsing context without
// an opener or referrer, and any other link degrades to plain text.
export function MetadataAttributionText({
  attribution,
  className,
}: {
  attribution: MetadataAttribution;
  className?: string;
}) {
  const locale = useDisplayLocale();
  const text = displayString(attribution.text, locale);
  if (!text) return null;
  if (!isHttpsURL(attribution.url)) return <span className={className}>{text}</span>;
  return (
    <a className={className} href={attribution.url} target="_blank" rel="noopener noreferrer">
      {text}
    </a>
  );
}
