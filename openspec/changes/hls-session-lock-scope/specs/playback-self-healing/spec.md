## ADDED Requirements

### Requirement: One session's encoder churn does not stall other sessions

The live HLS session manager SHALL NOT hold its global lock while it stops an encoder, waits for
an encoder to exit, spawns an encoder or removes a session's directory. A session whose encoder
is being replaced SHALL keep its transcode slot and lease until the replacement starts or fails.

#### Scenario: Slow encoder teardown

- **WHEN** one session's encoder is slow to exit while that session is torn down
- **THEN** segment requests for other sessions are served without waiting for it, and the torn
  down session's directory is removed once its encoder has exited

#### Scenario: Slow encoder replacement

- **WHEN** a far seek replaces a session's encoder and the old encoder is slow to exit
- **THEN** other sessions' requests are served meanwhile, and the replacing session still counts
  against the transcode cap

#### Scenario: Eviction during a replacement

- **WHEN** a session is torn down while its encoder is being replaced
- **THEN** the teardown does not wait for the replacement, the replacing request fails with the
  session-evicted error, and the session's lease and directory are released
