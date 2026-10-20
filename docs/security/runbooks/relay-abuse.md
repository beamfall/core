# Runbook: Relay abuse

Respond to relay over-capacity or abuse signals. (The relay itself is `beamfall-relay`; this runbook
covers the operator response from the Core/ops side.)

## Procedure
1. Detect: watch the relay over-cap signal (DEC-6 alert) — counter not yet emitted by core; wire
   when available (see [metrics](metrics.md)).
2. Throttle / shed: apply the relay's fair-use limits; if a single carrier token is abusive, rotate
   it (carrier-token hardening tracked in ORG-23).
3. Firewall: ensure the carrier port is restricted to known peers per the relay operator doc.
4. Escalate to [incident](incident.md) if abuse persists or indicates compromise.

## Targets
- Relay over-cap alert threshold + fair-use limits: alert on any sustained over-cap event once the
  counter exists; until then, derive over-cap from relay telemetry and operator logs.
- Carrier-token rotation cadence: rotate every 12 months or immediately on suspected compromise,
  matching DEC-6 key lifecycle defaults. ORG-23 hardens carrier-token mechanics.

## Anomaly-alert response (NGAUDIT-M-ANOMALYRUNBOOK)

ADR-0018 §4's anomaly alerts fire on a traffic-pattern signature — sustained multi-viewer fan-out
that looks like redistribution rather than personal remote access (`fair_use_soft_limit_crossed` /
`fair_use_hard_limit_crossed` in `beamfall-relay/internal/relay/fairuse.go`, keyed by pseudonymized
tunnel id per X-P18RELAY). The relay is byte-blind: an alert is a volume/stream-count signature, not
evidence of what was streamed. This response is **content-blind fair-use/capacity enforcement**; it
is never content adjudication, which the relay cannot perform.

1. **Alert fires.** The fair-use limiter crosses its soft or hard threshold for a tunnel and logs the
   disposition (pseudonymized tunnel id, window bytes, limit crossed).
2. **Fair-use review.** Compare the account's logged volume/stream-count pattern against the
   published AUP thresholds (~1–2 TB/month, 3 concurrent remote streams). No media path, title, or
   payload is inspected — only the metadata the relay already observes.
3. **Throttle-by-policy conversation.** Contact the account holder about the pattern under the
   published fair-use policy; apply the relay's existing throttle (`throttledMaxConns`) while the
   conversation is open.
4. **Account review.** If the pattern persists past the conversation, escalate to an account review
   under [legal-abuse-intake](legal-abuse-intake.md) §2.
5. **AUP termination.** For egregious or repeated fan-out consistent with reselling access, terminate
   hosted-relay access under the AUP (`/aup` §6). Termination affects hosted-relay access only; it
   never disables the user's self-hosted AGPL Core.

Log every alert's disposition (which stage it reached and the outcome) in the abuse ledger so the
record shows consistent enforcement across accounts. The public AUP (`/aup` §6) states this same
five-stage, content-blind rationale so the internal runbook and the published policy agree.
