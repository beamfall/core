# Runbook: Legal & abuse intake (DMCA, abuse@, CSAM)

Handle inbound legal and abuse notices for the hosted services (Beamfall Accounts + the optional
Beamfall Hosted Relay). This is the operator-facing counterpart to the public policy pages published
under `beamfall-web` (Terms, Privacy, Acceptable Use, Copyright/DMCA, CSAM). It covers the
`§512(i)` repeat-infringer process, the `abuse@` intake, and the `§2258A` CSAM protocol.

Capacity/carrier-token abuse (over-cap, fair-use throttling) is a separate concern — see
[relay-abuse](relay-abuse.md). This runbook is about **legal notices and content-abuse reports**,
not load.

> Legal framing here restates the public policies; it is operational guidance, not legal advice.
> Counsel sign-off (Engagement 2) gates go-live of the hosted relay. See "Human registration steps".

> **Scope (2026-07-04): Beamfall does not operate a first-party hosted relay.** The relay ships as
> self-hostable open source (AGPL); anyone may run their own. The **relay-operator duties** in this
> runbook — registering a designated DMCA agent for relay hostnames (§512(a) conduit), NCMEC/`§2258A`
> CyberTipline registration and CSAM reporting, `abuse@` intake for relay-carried traffic, and the
> counsel sign-off that gates a *public* relay — are therefore the responsibility of **each party who
> operates a public Beamfall relay**, to be completed before *they* expose their relay to real users.
> They are **not** a Beamfall first-party launch gate. (Beamfall Accounts, the proprietary cloud, is a
> separate first-party service that by invariant 7 never carries library/playback/watch content, so
> the content-conduit and `§2258A` duties do not attach to it.)

## Contacts & channels

- **`abuse@beamfall.com`** — general abuse intake, routed to a human. Delivery must be tested before
  hosted-relay go-live (see checklist).
- **`dmca@beamfall.com` / designated agent** — copyright notices. Listed in the U.S. Copyright
  Office DMCA directory (manual registration, $6, three-year renewal calendared).
- **CSAM responsible officer** — named individual who owns the `§2258A` protocol below.

## 1. DMCA copyright notice — `notice → log → forward → count → terminate`

Keyed to **relay accounts** and `<id>.relay.<domain>` hostnames (the relay is a §512(a) transitory
conduit — we route bytes, we do not host content).

1. **Notice.** Receive a takedown at the designated agent. Confirm it has the §512(c)(3) elements
   (work identified, material/hostname identified, contact info, good-faith statement, accuracy
   under penalty of perjury, signature). Incomplete notices: request the missing elements.
2. **Log.** Record the notice in the abuse ledger: timestamp, relay account id, hostname, complainant,
   work. This ledger is the evidence for the repeat-infringer count.
3. **Forward.** Forward the notice to the affected relay account holder. Because the relay is
   byte-blind, we cannot remove the *content* (it lives on the user's server); we act on the
   **account's relay access**.
4. **Count.** Increment the account's validated-infringement count. A counter-notice with the
   equivalent statutory elements is logged and pauses the count for that item.
5. **Terminate.** When an account crosses the repeat-infringer threshold, terminate its hosted-relay
   access and record the termination in the ledger. Termination affects hosted access only; it never
   disables the user's self-hosted AGPL Core.

## 2. `abuse@` general intake

1. Triage the report. If it alleges copyright → route to §1. If it alleges CSAM → go to §3
   immediately (do not wait).
2. For fair-use / capacity abuse, hand to [relay-abuse](relay-abuse.md).
3. Log all reports in the abuse ledger for pattern detection and repeat-actor tracking.

## 3. CSAM — actual-knowledge protocol (`§2258A`)

**Posture:** no proactive scanning (`§2258A(f)`); the byte-blind relay makes scanning impossible by
design. Library/playback never reach our cloud. We act only on **actual knowledge**.

On actual knowledge of apparent CSAM, the named responsible officer must:

1. **Preserve** the relevant material/metadata for **one year** (`§2258A(h)`).
2. **Report** to the NCMEC CyberTipline (NCMEC registration must be complete before hosted-relay
   go-live).
3. **Terminate** the responsible account.
4. **Cooperate** with law enforcement.

Do **not** proactively search other accounts in response — that would undercut the §2258A(f) posture.
Document each step in the abuse ledger with restricted access.

## Human registration steps (manual — gate hosted-relay go-live)

These are `[manual]` steps outside code; track them to completion before the hosted relay serves
traffic:

- [ ] Intermediary-liability counsel (Engagement 2) sign-off on the published Terms/Privacy/AUP/DMCA/CSAM pages.
- [ ] Register the designated DMCA agent with the U.S. Copyright Office directory ($6); calendar the
      3-year renewal.
- [ ] Register with NCMEC for CyberTipline reporting.
- [ ] Verify `abuse@beamfall.com` delivery routes to a monitored human inbox (send + confirm a test).
- [ ] Name the CSAM responsible officer and record them here and in the internal ledger.

Planned evidence path, created when NGAUDIT-B1 executes:
`docs/plans/roadmap/evidence/NGAUDIT-B1-legal-set.md`. <!-- doc-links-allow: future ticket evidence -->
