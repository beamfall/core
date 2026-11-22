# Access schedule ended: a distinct, client-readable denial

Tickets: CORE-2950 (`queue:core:main`)

## Why

When a managed Profile's access schedule closes, scope resolution refuses its session with
`auth.ErrAccessOutsideSchedule`. Every surface flattened that into the generic credential failure:
GraphQL answered 401 `UNAUTHENTICATED` "invalid session", playback-info answered 401, and the media
transports answered 401 or "stream ended". A client could not tell "your access time is over" from
"your session is gone", so it signed the viewer out or re-paired instead of showing that access has
ended for now. The session is still valid: an approved extension or the next window restores it.

## What changes

- One closed denial reason, `ACCESS_SCHEDULE_ENDED`, shared by every surface
  (`internal/auth/denial.go`).
- GraphQL: the request gate and the after-mutation scope re-resolution answer HTTP 403 with code
  `FORBIDDEN`, reason `ACCESS_SCHEDULE_ENDED` and message "access schedule ended". The reason joins
  the closed `extensions.reason` vocabulary.
- REST `playback-info` and `preview-info`: HTTP 403, header `X-Beamfall-Denial-Reason:
  ACCESS_SCHEDULE_ENDED`, body `{"error":"access schedule ended","reason":"ACCESS_SCHEDULE_ENDED"}`.
- Media transports (Direct, HLS, Subtitles including styled ASS, Sidecars, Assets): a Stream Token
  re-verify that fails only on the schedule answers HTTP 403 with the same header and a plain-text
  body.
- Every other refusal keeps its current status, code and message.
- The wire contract records the reason in `graphqlErrors.reasons` and in notes for GraphQL errors
  and playback-info. Publishing the new contract version is the lander's step.

## Non-goals

- No schedule detail (windows, next opening, time zone) in any denial.
- The Watch Party cross-member check, artwork, offline packs and encrypted-subtitle mid-stream
  re-verifies keep their generic refusals: the first would reveal another Profile's schedule, the
  rest are not playback-start surfaces.
- The proxy-tee transport is owned by the subscription work and follows separately.
- Client SDK (kit) and the web, Android and tvOS schedule-ended cards are their repos' follow-ups.
