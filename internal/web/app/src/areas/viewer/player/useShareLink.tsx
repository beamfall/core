import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n";

const TOAST_MS = 2500;

// Legacy copy for non-secure origins (plain-HTTP LAN servers), where
// navigator.clipboard is undefined.
function execCopy(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.cssText = "position:fixed;top:0;left:0;opacity:0;";
  document.body.appendChild(area);
  try {
    area.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
  }
}

async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to the legacy path
  }
  return execCopy(text);
}

/**
 * Shares a link without ever failing silently: native share sheet, then the
 * clipboard, then a manual-copy dialog. `node` must be rendered by the caller.
 */
export function useShareLink(title: string): { share: (href: string) => void; node: ReactNode } {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const [manual, setManual] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const share = useCallback(
    (href: string) => {
      void (async () => {
        if (typeof navigator.share === "function") {
          try {
            await navigator.share({ title, url: href });
            return;
          } catch (error) {
            if (error instanceof DOMException && error.name === "AbortError") return;
          }
        }
        if (await copyText(href)) {
          setCopied(true);
          window.clearTimeout(timer.current);
          timer.current = window.setTimeout(() => setCopied(false), TOAST_MS);
        } else {
          setManual(href);
        }
      })();
    },
    [title],
  );

  const node = (
    <>
      {copied && (
        <div className="bf-remote-message" role="status">
          <p className="bf-remote-message-text">{t("shareLinkCopied")}</p>
        </div>
      )}
      <Dialog open={manual !== null} onOpenChange={(open) => !open && setManual(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("shareLinkTitle")}</DialogTitle>
            <DialogDescription>{t("shareLinkManual")}</DialogDescription>
          </DialogHeader>
          <Input
            readOnly
            value={manual ?? ""}
            aria-label={t("shareLinkTitle")}
            onFocus={(event) => event.currentTarget.select()}
            ref={(el) => el?.select()}
          />
          <DialogFooter>
            <Button type="button" onClick={() => setManual(null)}>
              {t("shareLinkClose")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
  return { share, node };
}
