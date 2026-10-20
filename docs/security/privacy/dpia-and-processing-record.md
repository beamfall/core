# DPIA & Record of Processing Activities — Beamfall cloud (NGAUDIT-C1)

**Status:** engineering draft for counsel review · **Owner:** Beamfall (data controller) ·
**Date:** 2026-07-03 · **Ticket:** NGAUDIT-C1 · **Follow-up (human/legal):** NGAUDIT-C1-MANUAL

> Scope note. This document is the **engineering-authored** Data Protection Impact Assessment
> (GDPR Art. 35) and Record of Processing Activities (GDPR Art. 30) for the **proprietary Beamfall
> cloud** (Accounts, entitlement ledger, Hosted Relay metadata, device-link, push routing) and the
> `beamfall.com` marketing/portal site. It does **not** cover the AGPL Core media server, which holds
> no cloud-side personal data (invariant #7: the account knows what you bought, never what you watch;
> library/playback/viewing data never leaves the self-hosted server).
>
> The **human/legal steps** — counsel review and sign-off of this DPIA, DPA execution with Render and
> AWS, and any regulator/DPO filing — are tracked separately as **NGAUDIT-C1-MANUAL** and are *not*
> represented as done by this document.

Binding context: ADR-0024 (Accounts/entitlement boundary), ADR-0036 (My Data export/delete spirit),
ADR-0103 (relay metadata retention & legal-process posture), ADR-0034 (EN + fr-CA bilingual).

---

## 1. Art. 30 — Record of Processing Activities

| # | Processing activity | Personal data categories | Data subjects | Purpose | Lawful basis (Art. 6) | Recipients / processors | Transfer | Retention |
|---|---|---|---|---|---|---|---|---|
| P1 | Beamfall Account (sign-in, entitlement) | Email; purchase/entitlement state; linked-device list | Account holders | Provision account + Premium entitlement | Contract (6(1)(b)) | Render, AWS | US (SCCs) | Until account deletion (ledger tombstoned, Art. 17-compatible) |
| P2 | Entitlement ledger | Account id, entitlement id, generic channel label, class, effective dates (never a plugin/product id) | Account holders | Durable system of record for issuance | Contract (6(1)(b)); legal obligation for tax/refund records (6(1)(c)) | Render, AWS | US (SCCs) | Append-only; tombstoned + anonymized on deletion |
| P3 | Hosted Relay metadata | SNI/hostname, connection metadata, edge/source IP, region/node, volume, duration (byte-blind — no content) | Relay users | Route + operate the tunnel; abuse/fair-use | Legitimate interests (6(1)(f)) | Render/edge | US (SCCs) | Transient — max window in days (ADR-0103); enforced by `RetentionJob` |
| P4 | Device-link / device-code | User code, app family, link timestamp | Account holders | Link clients to the account | Contract (6(1)(b)) | Render, AWS | US (SCCs) | Until device signed out or account deleted |
| P5 | Push routing | Push token, device/app family | Account holders | Deliver account/entitlement notifications | Contract (6(1)(b)) | APNs / FCM | US (SCCs) | Until token invalidated / account deleted |
| P6 | Website security metadata | CSRF tokens, rate-limit counters, edge IPs | Site visitors | Operate the site safely | Legitimate interests (6(1)(f)) | Render | US (SCCs) | Transient (session / short-lived) |
| P7 | Marketing waitlist | Email address | Prospects | Waitlist / launch comms | Consent (6(1)(a)) | Render, email provider | US (SCCs) | Until unsubscribe / launch |

**Controller:** Beamfall. **Categories of personal data explicitly *not* processed in the cloud:**
library contents, media titles, playback/viewing history, watch positions — these live only on the
user's self-hosted server and never reach Beamfall (design-enforced, invariant #7).

---

## 2. Art. 35 — DPIA

### 2.1 Why a DPIA (screening)

Processing includes systematic account/payment linkage and relay connection metadata that is a
potential **viewing-behavior fingerprint** (ADR-0103). Although no content is processed, the metadata
+ identity combination warrants a DPIA per the Art. 35 scope in the privacy report: account/ledger, relay metadata, device-link,
push routing.

### 2.2 Necessity & proportionality

- **Data minimisation.** The account stores what you bought, never what you watch. The ledger carries
  a fixed vocabulary (class + generic channel) and drops provider product ids after mapping
  (regression-gated by
  `beamfall-cloud/internal/entitlement/privacy_vocab_test.go`).
- **Storage limitation.** Relay/operational logs are pruned to a published maximum window "measured in
  days, not months," enforced by `entitlement.RetentionJob` (fail-closed default 14 days).
- **Metadata firewall.** Relay observability is metadata-only and is **not joined** to the account or
  entitlement ledger (ADR-0103).

### 2.3 Risks and mitigations

| Risk | Likelihood/impact | Mitigation | Residual |
|---|---|---|---|
| Relay metadata re-identifies viewing behavior | Med / High | Byte-blind relay (ADR-0018/0046); metadata not joined to account; short retention (ADR-0103, `RetentionJob`) | Low |
| Provider webhook leaks a plugin/product id into the ledger | Low / High | Fixed-vocabulary mapping; `privacy_vocab_test.go` regression gate | Low |
| Account data exfiltrated via hijacked session (DSAR export) | Low / Med | Step-up required for export + deletion (`ActionAccountDataExport`, `ActionAccountDeletion`) | Low |
| International transfer without safeguard | Med / High | SCCs + UK IDTA in DPAs with Render/AWS (**pending signature — NGAUDIT-C1-MANUAL**) | Med until DPA signed |
| Data subject cannot exercise rights | Med / Med | DSAR channel + process (privacy@beamfall.com), `ExportAccount` + `TombstoneAccount` seams, 30/45-day SLA | Low |

### 2.4 Data-subject rights implementation

- **Access / portability (Art. 15/20):** `entitlement.ExportAccount` + portal account view, surfaced
  via `POST /api/accounts/account/data-export` and the "My account data" export control.
- **Erasure (Art. 17):** `entitlement.TombstoneAccount` (anonymize derived state, append-only
  tombstone, provider unlink) via `POST /api/accounts/account/delete`.
- **Channel & SLA:** privacy@beamfall.com / `/privacy#dsar`; respond within 30 days (GDPR) / 45 days
  (CCPA), one extension where lawful.

---

## 3. Open human/legal actions (NGAUDIT-C1-MANUAL)

1. EU+US privacy counsel review and sign-off of this DPIA and the completed privacy policy.
2. Execute DPAs (with SCCs / UK IDTA) with Render and AWS; record signature + sub-processor list.
3. Adopt counsel's qualifying language for the "never what you watch" claim (FTC §5).
4. Publish the EU accessibility statement alongside the legal set; confirm DPO/authority contacts.

These are legal actions requiring a human signatory and are **not** satisfied by this document.
