# Styled subtitle resources v1

A playback response advertises a muxed ASS or SSA track with a tokenized WebVTT
URL at `/api/media/subtitle/{token}/{track}.vtt`. The served web player can
derive these read-only resources from the same URL, including its base path.

| GET path suffix | Successful response |
| --- | --- |
| `/{track}.ass` | `text/x-ssa; charset=utf-8`, a styled ASS script remuxed from the source track |
| `/{track}.ass/fonts` | `application/json`, an array of absolute stream indexes for eligible Matroska font attachments, or `[]` |
| `/{track}.ass/fonts/{attachment}` | `application/octet-stream`, the font bytes from that same file |

The token must be a live subtitle grant bound to the exact file fingerprint and
subtitle stream. The server rechecks the session and file visibility before
serving bytes. Invalid grants return 401 or 410 under the existing subtitle
rules. Unavailable tracks and ineligible attachments return 404. Responses carry
`Cache-Control: private, no-store`.

Attachments must be Matroska attachment streams with a supported font MIME
type and font signature. Font responses are limited to 3 MiB each. The manifest
lists up to 32 fonts totaling up to 24 MiB. Encrypted files use their existing
WebVTT path. Unavailable styled resources leave the original .vtt URL usable.

An external ASS or SSA sidecar continues to use the existing
`/api/media/sidecar/{token}/{index}.ass` passthrough. Its corresponding `.vtt`
URL provides the WebVTT fallback.
