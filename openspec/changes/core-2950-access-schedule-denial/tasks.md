- [x] `auth.DenialReasonHeader`, `auth.DenialReasonAccessScheduleEnded` and `auth.AccessScheduleEnded`
- [x] GraphQL gate and after-mutation re-resolution map the schedule refusal to 403 `FORBIDDEN` /
      `ACCESS_SCHEDULE_ENDED`; the reason joins the closed vocabulary
- [x] playback-info and preview-info answer 403 with the header and reason body
- [x] Direct, HLS, Subtitles (and styled ASS), Sidecars and Assets answer 403 with the header
- [x] Graph, server and mediahttp tests; conformance vocabulary list updated
- [x] Wire contract reasons and notes; goldens regenerated with `make regen-wire-goldens`
- [ ] Lander: publish the next client-core-wire minor and repin consumers
- [ ] Follow-ups: kit SDK exposes the reason; web, Android and tvOS schedule-ended cards
