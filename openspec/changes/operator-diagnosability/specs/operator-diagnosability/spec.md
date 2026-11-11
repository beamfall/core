## ADDED Requirements

### Requirement: Every request produces one structured access line

The server SHALL emit one `http request` log record for every HTTP request, after the response,
carrying the request ID, method, matched route pattern (or `unmatched`), the request path with
any Stream Token segment redacted and without the query string, the response status, the latency
in milliseconds and the presented credential class (`anonymous`, `session`, `api_token`,
`scoped_key` or `stream_token`). The record SHALL NOT carry any credential value.

#### Scenario: Routed request

- **WHEN** a client calls `GET /health`
- **THEN** one `http request` record names the request ID from the response header, route
  `GET /health`, status 200, a latency and principal `anonymous`

#### Scenario: Token-bearing path

- **WHEN** a client requests a media byte route whose path carries a Stream Token and whose query
  carries a secret
- **THEN** the access record's path shows `[redacted]` in place of the token, carries no query
  string, and names principal `stream_token`

### Requirement: Every response carries a request ID

Every response SHALL carry the `X-Beamfall-Request-Id` header, including responses refused before
routing, and the access record SHALL carry the same ID.

#### Scenario: Refused before routing

- **WHEN** a request is refused by the access policy before it reaches a route
- **THEN** the response still carries `X-Beamfall-Request-Id`

### Requirement: Request metrics have bounded labels

`/metrics` SHALL report `beamfall_http_requests_total{status_class}` for each of `1xx`, `2xx`,
`3xx`, `4xx` and `5xx`, always emitting all five, and SHALL report
`beamfall_http_request_duration_seconds_sum` and `beamfall_http_request_duration_seconds_count`.
No request metric SHALL carry a path, route, principal or identifier label.

#### Scenario: Status classes counted

- **WHEN** a client makes one request answered 200 and one answered 404, then scrapes `/metrics`
- **THEN** the `2xx` and `4xx` counters each include those requests and all five classes appear

### Requirement: Credential refusals produce an audit record without secret material

When the server refuses a presented credential, it SHALL emit one `auth refused` record for the
request that bypasses the log-level filter and SHALL increment
`beamfall_auth_refusals_total{reason}`. The record SHALL carry the operation, a reason from a
closed vocabulary, the credential class, method, route and request ID, and SHALL NOT carry a
session ID, device secret, token, key, password or pairing code.

#### Scenario: Wrong device secret

- **WHEN** a client presents a valid session and device ID with a wrong device secret and is
  answered 401
- **THEN** one `auth refused` record with reason `device_secret_invalid` and credential `session`
  is emitted, the secret appears neither in the logs nor in `/metrics`, and
  `beamfall_auth_refusals_total{reason="device_secret_invalid"}` increases

#### Scenario: Wrong pairing code

- **WHEN** a client calls `POST /api/enroll` with a pairing code that does not verify
- **THEN** one `auth refused` record with operation `enroll` and reason `pairing_code_invalid` is
  emitted and the code does not appear in it

### Requirement: Refusal records follow the request outcome

A scope-resolution refusal SHALL be recorded only when the response status is 401 or 403. An
explicit sign-in attempt (admin re-auth, integration sign-in, device enrollment) SHALL be recorded
whenever it is refused. A request that presents no credential SHALL produce no refusal record.

#### Scenario: Anonymous request

- **WHEN** a client with no credential calls a route that requires one
- **THEN** no `auth refused` record is emitted

#### Scenario: Refusal on a request that still succeeds

- **WHEN** scope resolution refuses a credential but the response is not 401 or 403
- **THEN** no `auth refused` record is emitted

### Requirement: Health derives status from readiness and reports FFmpeg presence

`/health` SHALL report `status` as `ok` when `ready` is true and `not_ready` otherwise, and SHALL
report boolean `ffmpeg` and `ffprobe` fields saying whether each binary resolves on the server's
`PATH`. FFmpeg presence SHALL NOT change `ready` or the response status.

#### Scenario: Not ready

- **WHEN** the readiness source reports an error
- **THEN** `/health` answers 503 with `ready:false` and `status:"not_ready"`

#### Scenario: FFmpeg missing

- **WHEN** the store is ready but `ffmpeg` does not resolve
- **THEN** `/health` answers 200 with `ready:true`, `status:"ok"` and `ffmpeg:false`
