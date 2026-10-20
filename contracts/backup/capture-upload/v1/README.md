# Backup Capture/Upload v1

Core owns this contract for backup-capable native clients. Client kits vendor
`schema.json` and `fixtures.json` one way, then prove their background-transfer
queue honors ADR-0045 section 2.11 while uploading to `/v1/backups/media`.

The endpoint remains the server byte-ingest boundary. This artifact covers the
client capture loop around it: network preference, pause reasons, progress,
resumability, dedup no-op handling, and delete-from-phone gating after confirmed
byte-exact storage.
