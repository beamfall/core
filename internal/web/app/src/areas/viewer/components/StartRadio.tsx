import { Radio } from "lucide-react";
import { useCallback, useState } from "react";
import { useNavigate } from "react-router";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { buttonVariants } from "@/components/ui/button";
import { getExpectedProfile } from "@/lib/api";
import type { MediaCredit } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { mediaPath } from "../lib/mediaAddress";
import { MUSIC_QUEUE_PARAM } from "../lib/musicQueue";
import {
  type RadioRules,
  type RadioSeedKind,
  type RadioStation,
  startRadio,
  stopRadio,
} from "../lib/radio";

interface Seed {
  seedKind: RadioSeedKind;
  seedID: string;
  rules?: RadioRules;
  ruleTitles?: Record<string, string>;
}

/**
 * Starts radio from one item (CORE-2772) and opens the player on the
 * Profile's Play Queue. A queue holding the listener's own items is replaced
 * only after they confirm. `element` renders the confirmation and any failure
 * and must be mounted by the caller.
 */
export function useStartRadio() {
  const t = useT();
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const [pending, setPending] = useState(false);
  const [confirming, setConfirming] = useState<Seed | null>(null);
  const [message, setMessage] = useState("");

  const start = useCallback(
    async (seed: Seed, confirmReplace = false) => {
      // The Profile this tab's requests name owns the queue radio fills.
      const profileID = getExpectedProfile();
      if (!profileID) return;
      setPending(true);
      setMessage("");
      try {
        const result = await startRadio({ ...seed, profileID, confirmReplace });
        if (result.status === "cancelled") return;
        if (result.status === "needs-confirm") setConfirming(seed);
        else if (result.status === "empty") setMessage(result.message || t("radioFailed"));
        else {
          const query = new URLSearchParams({ [MUSIC_QUEUE_PARAM]: profileID });
          void navigate(`${mediaPath("watch", result.firstMediaID)}?${query}`);
        }
      } catch {
        setMessage(t("radioFailed"));
      } finally {
        setPending(false);
      }
    },
    [navigate, t],
  );

  const element = (
    <>
      {message && (
        <span role="status" className="bf-card-action-error">
          {message}
        </span>
      )}
      <AlertDialog open={confirming !== null} onOpenChange={(open) => !open && setConfirming(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("radioReplaceTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("radioReplaceBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const seed = confirming;
                setConfirming(null);
                if (seed) void start(seed, true);
              }}
            >
              {t("radioReplaceConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );

  return { start, pending, available: !!identity, element };
}

/** The Start radio action on an Entity's detail page. */
export function StartRadioButton({ entityID }: { entityID: string }) {
  const t = useT();
  const radio = useStartRadio();
  if (!radio.available) return null;
  return (
    <>
      <button
        type="button"
        className={`${buttonVariants({ variant: "ghost", size: "lg" })} bf-detail-secondary`}
        disabled={radio.pending}
        onClick={() => void radio.start({ seedKind: "entity", seedID: entityID })}
      >
        <Radio aria-hidden />
        {radio.pending ? t("radioStarting") : t("radioStart")}
      </button>
      {radio.element}
    </>
  );
}

const DECADES = [1950, 1960, 1970, 1980, 1990, 2000, 2010, 2020];

/**
 * Station rules in the player while radio plays: keep to one decade, or to
 * Media credited with one of the playing item's categories. Changing a rule
 * restarts the Station with it, asking first as Start radio does: the queue
 * may hold items this tab never saw added.
 */
export function RadioTuning({
  station,
  credits,
}: {
  station: RadioStation;
  credits: readonly MediaCredit[];
}) {
  const t = useT();
  const radio = useStartRadio();
  const decade = station.rules.decades?.[0];
  const only = station.rules.anyEntityIDs?.[0] ?? "";
  const categories = credits.filter((c) => c.archetype === "category" && c.entityID);
  const options =
    categories.some((c) => c.entityID === only) || !only
      ? categories
      : [...categories, { entityID: only, title: station.ruleTitles?.[only] ?? only }];
  const restart = (rules: RadioRules, titles?: Record<string, string>) =>
    void radio.start({
      seedKind: station.seedKind,
      seedID: station.seedID,
      rules,
      ruleTitles: titles,
    });
  return (
    <div className="bf-radio-tuning">
      <Radio aria-hidden size={14} />
      <label>
        <span className="sr-only">{t("radioDecade")}</span>
        <select
          value={decade ?? ""}
          disabled={radio.pending}
          onChange={(event) => {
            const value = Number(event.target.value);
            restart({ ...station.rules, decades: value ? [value] : undefined }, station.ruleTitles);
          }}
        >
          <option value="">{t("radioDecadeAny")}</option>
          {DECADES.map((d) => (
            <option key={d} value={d}>
              {t("radioDecadeValue", { decade: d })}
            </option>
          ))}
        </select>
      </label>
      {options.length > 0 && (
        <label>
          <span className="sr-only">{t("radioOnly")}</span>
          <select
            value={only}
            disabled={radio.pending}
            onChange={(event) => {
              const id = event.target.value;
              const title = options.find((c) => c.entityID === id)?.title ?? "";
              restart(
                { ...station.rules, anyEntityIDs: id ? [id] : undefined },
                id ? { [id]: title } : undefined,
              );
            }}
          >
            <option value="">{t("radioOnlyAny")}</option>
            {options.map((c) => (
              <option key={c.entityID} value={c.entityID}>
                {c.title}
              </option>
            ))}
          </select>
        </label>
      )}
      <button
        type="button"
        className={buttonVariants({ variant: "ghost", size: "sm" })}
        onClick={stopRadio}
      >
        {t("radioStop")}
      </button>
      {radio.element}
    </div>
  );
}
