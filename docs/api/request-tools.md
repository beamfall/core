# Request-tool integration

Request-management tools take a user's request for an item, hand it to a download
manager and tell the user when the item is ready to watch. Beamfall has no native request
queue. It gives such a tool the four pieces it needs on stable surfaces: user
sign-in, an availability check, a targeted scan and an arrival signal. The tool owns
the requests themselves.

Core is domain-agnostic. An external identifier is an opaque `scheme` and `value`
pair, such as `example.db` and `item-7`, attached by hydration or a plugin. Any
provider meaning, for example one identifier space that covers two kinds of
item, belongs in the domain plugin or the tool, not in Core.

## Credentials

| Need | Surface | Credential |
|---|---|---|
| Sign a user in | Jellyfin compat `POST /compat/jellyfin/Users/AuthenticateByName` | The user's own Beamfall sign-in |
| Check availability, read Media | `GET /v1/media?scheme=&value=`, `GET /v1/media/{mediaID}` | Scoped key with `read-catalog`, or an enrolled device Session |
| Find Libraries and roots | `GET /v1/libraries` | Scoped key with `read-catalog` |
| Trigger a targeted scan | `POST /v1/scans` | Scoped key with `ingest` |
| Learn that an item arrived | `GET /v1/events` (SSE) or the Webhook Emitter | Scoped key with `events-subscribe`, or an operator-configured webhook endpoint |

The server operator mints one scoped Developer API key for the tool from the
Console with `read-catalog`, `ingest` and `events-subscribe`. Leave it unbound
to a Profile so availability answers cover every Library the tool should see.
Bind it to a Profile only when the tool serves that Profile alone. A compat
sign-in proves who the user is. It grants no `/v1` scopes.

## Flow

1. **Sign the user in.** The tool posts the user's name and password to the compat
   `AuthenticateByName` binding and keeps the returned user identity to attribute
   requests.
2. **Check availability.** Before accepting a request, look the item up by the
   identifier the tool already has:

   ```http
   GET /v1/media?scheme=example.db&value=item-7
   Authorization: Bearer bfk_...
   ```

   ```json
   {"media":[{"id":"media-alpha","libraryID":"library-main","title":"Example","year":2024,"externalIDs":[{"scheme":"example.db","value":"item-7"}]}]}
   ```

   A non-empty `media` list means the item is available. `{"media":[]}` means no
   Media the key can see carries the identifier. Hidden Media are left out exactly as
   absent ones, so an empty list never says whether hidden content exists. The
   lookup returns at most 20 Media. An identifier normally names one Media, but
   duplicate imports can share one until a dedupe pass resolves them.
3. **Trigger a targeted scan.** When the download manager finishes, scan only the
   affected Library, and only the root the file landed under when the Library has
   several:

   ```http
   POST /v1/scans
   Authorization: Bearer bfk_...
   Content-Type: application/json

   {"libraryID":"library-main","root":"/media/library-main"}
   ```

   `root` must be one of the Library's configured roots. Leave it empty to scan
   every root. The server answers `202` with the queued `jobIDs`.
4. **Wait for arrival.** Subscribe to `GET /v1/events`, or configure a Webhook
   Emitter endpoint for `media.created` and `media.updated`. Each event names the
   Media in `aggregateID` (`aggregateType` is `media`). On each one, read
   `GET /v1/media/{mediaID}` and match its `externalIDs` against open requests.
   A scan imports a file first and hydration may attach identifiers later. Each
   newly attached identifier emits one `media.updated`, so a tool that missed the
   match on `media.created` still sees it.
5. **Reconcile after downtime.** Events resume from `Last-Event-ID` or `?after=`
   within the stream's retention. After a longer outage, re-run the availability
   check for every open request.

See [`home-automation.md`](home-automation.md) for the event envelope, the
compatibility promise and the webhook delivery rules, and
[`reference.md`](reference.md) for every route's full schema.
