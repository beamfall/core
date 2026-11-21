# Play the server queue in the web audio player

Ticket CORE-2767, rescoped 2026-11-20.

The web audio player gains a read-only Play Queue panel and an explicit action to start playback
from the active Profile's server queue. The watch route identifies the current Media and the
Profile that started the queue run. Album playback and Play all retain their existing behavior.
The change uses existing reads and makes no queue edits.
