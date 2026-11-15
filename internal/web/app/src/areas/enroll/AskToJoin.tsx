import { useCallback, useEffect, useId, useRef, useState } from "react";
import { PrimaryAction } from "@/components/beamfall/surface";
import { ApiError } from "@/lib/api";
import {
  type JoinRequest,
  pollJoinRequest,
  type RebindOutcome,
  regenerateJoinRequest,
  requestToJoin,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { FormFeedback } from "./AuthShell";

// Ask to join (CORE-2070): the browser asks the server for a short code,
// displays it, and polls until an administrator approves it in the Console
// (docs/api/lan-pairing.md). Nothing is typed. The poll carries the CSRF
// header, so on approval the server sets the same credential cookies typed
// enrollment gets and the page reloads to BASE_URL the same way.
//
// The request secret is the browser's only handle on the request and the
// thing that collects the credential: it lives in a ref for the life of one
// attempt and is never stored, put in a URL, rendered or logged. Every
// request, poll and regenerate belongs to one numbered attempt; anything that
// answers for an older attempt is ignored, and unmounting aborts the attempt.

export type AskFailure = "denied" | "expired" | "throttled" | "refused" | "insecure" | "offline";

export type AskState =
  | { phase: "idle" }
  | { phase: "unsupported" }
  | { phase: "requesting" }
  | { phase: "waiting"; code: string; renewing: boolean }
  | { phase: "approved"; rebind: RebindOutcome }
  | { phase: "failed"; reason: AskFailure };

export interface AskToJoin {
  state: AskState;
  start: (deviceName: string) => void;
  regenerate: () => void;
  cancel: () => void;
}

const DEFAULT_POLL_SECONDS = 3;

export function useAskToJoin(): AskToJoin {
  const [state, setState] = useState<AskState>({ phase: "idle" });
  const attempt = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const secret = useRef<string | null>(null);
  const timer = useRef<number | undefined>(undefined);

  // Ends the current attempt: later answers for it are stale.
  const stop = useCallback(() => {
    attempt.current += 1;
    controller.current?.abort();
    controller.current = null;
    secret.current = null;
    window.clearTimeout(timer.current);
    timer.current = undefined;
  }, []);

  // A fresh attempt: its number and the signal that aborts it.
  const next = useCallback(() => {
    stop();
    const signal = new AbortController();
    controller.current = signal;
    return { id: attempt.current, signal: signal.signal };
  }, [stop]);

  const fail = useCallback(
    (reason: AskFailure) => {
      stop();
      setState({ phase: "failed", reason });
    },
    [stop],
  );

  const wait = useCallback(
    (ticket: JoinRequest, id: number, signal: AbortSignal) => {
      secret.current = ticket.requestSecret;
      const expiresAt = Date.parse(ticket.expiresAt);
      const seconds =
        ticket.pollIntervalSeconds > 0 ? ticket.pollIntervalSeconds : DEFAULT_POLL_SECONDS;
      setState({ phase: "waiting", code: ticket.code, renewing: false });

      const schedule = () => {
        timer.current = window.setTimeout(() => void tick(), seconds * 1000);
      };
      const tick = async () => {
        if (id !== attempt.current || secret.current === null) return;
        if (!Number.isNaN(expiresAt) && Date.now() >= expiresAt) return fail("expired");
        try {
          const answer = await pollJoinRequest(secret.current, signal);
          if (id !== attempt.current) return;
          if (answer.status === "approved") {
            stop();
            setState({ phase: "approved", rebind: answer.rebind });
            window.location.assign(import.meta.env.BASE_URL);
          } else if (answer.status === "denied") {
            fail("denied");
          } else {
            schedule();
          }
        } catch (err) {
          if (id !== attempt.current) return;
          const reason = refusal(err);
          // A dropped connection or a server error is retried until expiry.
          if (reason === null) schedule();
          else fail(reason === "refused" ? "expired" : reason);
        }
      };
      schedule();
    },
    [fail, stop],
  );

  const start = useCallback(
    (deviceName: string) => {
      const { id, signal } = next();
      setState({ phase: "requesting" });
      requestToJoin(deviceName, signal).then(
        (ticket) => {
          if (id === attempt.current) wait(ticket, id, signal);
        },
        (err: unknown) => {
          if (id !== attempt.current) return;
          if (err instanceof ApiError && err.status === 404) {
            stop();
            setState({ phase: "unsupported" });
            return;
          }
          fail(refusal(err) ?? "offline");
        },
      );
    },
    [fail, next, stop, wait],
  );

  const regenerate = useCallback(() => {
    const old = secret.current;
    if (old === null) return;
    const { id, signal } = next();
    setState((current) => (current.phase === "waiting" ? { ...current, renewing: true } : current));
    regenerateJoinRequest(old, signal).then(
      (ticket) => {
        if (id === attempt.current) wait(ticket, id, signal);
      },
      (err: unknown) => {
        if (id !== attempt.current) return;
        const reason = refusal(err);
        fail(reason === "refused" ? "expired" : (reason ?? "offline"));
      },
    );
  }, [fail, next, wait]);

  const cancel = useCallback(() => {
    stop();
    setState((current) => (current.phase === "unsupported" ? current : { phase: "idle" }));
  }, [stop]);

  useEffect(() => stop, [stop]);

  return { state, start, regenerate, cancel };
}

// 401 at request time means the server is not taking requests; at poll or
// regenerate time it means the request expired or was spent (callers map it).
function refusal(err: unknown): AskFailure | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status === 401) return "refused";
  if (err.status === 429) return "throttled";
  if (err.status === 403) return "insecure";
  return err.status >= 500 ? null : "refused";
}

const FAILURE_MESSAGE = {
  denied: "askToJoinDenied",
  expired: "askToJoinExpired",
  throttled: "askToJoinThrottled",
  refused: "askToJoinRefused",
  insecure: "adminSignInNeedsSecure",
  offline: "couldNotReachServer",
} as const;

const SECONDARY = "w-full border-foreground/15 bg-transparent hover:bg-foreground/[0.06]";

export function AskToJoinPanel({
  ask,
  disabled,
  onStart,
}: {
  ask: AskToJoin;
  disabled: boolean;
  onStart: () => void;
}) {
  const t = useT();
  const errorID = useId();
  const codeID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const { state } = ask;
  if (state.phase === "unsupported") return null;

  const error = state.phase === "failed" ? t(FAILURE_MESSAGE[state.reason]) : null;
  const status =
    state.phase === "approved"
      ? t(state.rebind === "bound" ? "enrollConnected" : "enrollHandshakeIncomplete")
      : state.phase === "waiting"
        ? t("askToJoinWaiting")
        : null;

  return (
    <div className="mt-5">
      <p className="m-0 mb-5 flex items-center gap-3 text-xs uppercase tracking-wide text-muted-foreground before:h-px before:flex-1 before:bg-foreground/10 after:h-px after:flex-1 after:bg-foreground/10">
        {t("adminSignInOr")}
      </p>
      {state.phase === "waiting" || state.phase === "approved" ? (
        <div className="space-y-4">
          {state.phase === "waiting" && (
            <>
              <div className="text-center">
                <p id={codeID} className="m-0 mb-2 text-dense font-medium text-foreground/80">
                  {t("askToJoinCodeLabel")}
                </p>
                <p
                  aria-labelledby={codeID}
                  className="m-0 font-mono text-3xl font-semibold tracking-[0.3em]"
                >
                  {state.code}
                </p>
              </div>
              <p className="m-0 text-center text-sm leading-relaxed text-muted-foreground">
                {t("askToJoinApproveInConsole")}
              </p>
            </>
          )}
          <FormFeedback status={status} error={null} errorID={errorID} errorRef={errorRef} />
          {state.phase === "waiting" && (
            <div className="grid grid-cols-2 gap-3">
              <PrimaryAction
                type="button"
                variant="outline"
                disabled={state.renewing}
                onClick={ask.regenerate}
                className={SECONDARY}
              >
                {t("askToJoinNewCode")}
              </PrimaryAction>
              <PrimaryAction
                type="button"
                variant="outline"
                onClick={ask.cancel}
                className={SECONDARY}
              >
                {t("cancel")}
              </PrimaryAction>
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          <FormFeedback error={error} errorID={errorID} errorRef={errorRef} />
          <PrimaryAction
            type="button"
            variant="outline"
            disabled={disabled || state.phase === "requesting"}
            onClick={onStart}
            className={SECONDARY}
          >
            {t("askToJoin")}
          </PrimaryAction>
          <p className="m-0 text-dense text-muted-foreground">{t("askToJoinHint")}</p>
        </div>
      )}
    </div>
  );
}
