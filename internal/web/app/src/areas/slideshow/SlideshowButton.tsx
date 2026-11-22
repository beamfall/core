import { Presentation } from "lucide-react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import type { SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { hasImages, type SlideshowSource, slideshowPath } from "./lib/slideshow";

/** The Slideshow action for a page whose listed items include an image (CORE-2981). */
export function SlideshowButton({
  source,
  items,
}: {
  source: SlideshowSource;
  items: readonly SourceItem[] | undefined;
}) {
  const t = useT();
  const navigate = useNavigate();
  if (!hasImages(items)) return null;
  return (
    <Button variant="outline" onClick={() => navigate(slideshowPath(source))}>
      <Presentation aria-hidden />
      {t("slideshowAction")}
    </Button>
  );
}
