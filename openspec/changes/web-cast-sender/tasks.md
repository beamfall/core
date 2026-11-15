## 1. Web

- [x] 1.1 Lazy, gated Cast SDK loader (`cast/castSdk.ts`)
- [x] 1.2 Default Media Receiver profile, playback-info request and receiver load (`cast/castMedia.ts`)
- [x] 1.3 Sender state and RemotePlayerController wiring (`cast/useCast.ts`)
- [x] 1.4 Cast button and cast controls, mounted in `PlayerTransport.tsx`; strings in `strings/cast.ts`
- [x] 1.5 vitest against a mocked Cast framework

## 2. Server

- [x] 2.1 CSP `script-src` for the sender's `www.gstatic.com` paths, with a Go test

## 3. Follow-ups (not in this change)

- [ ] 3.1 Quirks-DB selector for Cast receivers so receiver-bound bytes resolve (ADR-0194 section 6,
  ADR-0089 receiver-trusted TLS), with a security review
- [ ] 3.2 Test on a physical Cast receiver, including the CSP paths the framework loads
- [ ] 3.3 Drive the receiver from the main transport and keyboard shortcuts while casting
- [ ] 3.4 Subtitle and audio switching mid-cast
- [ ] 3.5 Native-client Cast senders

## 4. Gate and archive

- [ ] 4.1 `make gate`
- [ ] 4.2 Archive the change
