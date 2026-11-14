## ADDED Requirements

### Requirement: One session's encoder churn does not stall other sessions

The live HLS session manager SHALL NOT hold its global lock while it stops an encoder, waits for
an encoder to exit, spawns an encoder or removes a session's directory.

#### Scenario: Slow encoder teardown

- **WHEN** one session's encoder is slow to exit while that session is torn down
- **THEN** segment requests for other sessions are served without waiting for it, and the torn
  down session's directory is removed once its encoder has exited

#### Scenario: Slow encoder replacement

- **WHEN** a far seek replaces a session's encoder and the old encoder is slow to exit
- **THEN** other sessions' requests are served meanwhile, and the replacing session still counts
  against the transcode cap

### Requirement: Encoder work done outside the lock still counts

A session whose encoder is being replaced SHALL keep its transcode slot and lease until the
replacement starts or fails, and SHALL NOT be preempted meanwhile. A torn-down session SHALL keep
its slot and lease until its encoder has stopped. Shutdown SHALL NOT return while an encoder start
or teardown is still in progress.

#### Scenario: Eviction during a replacement

- **WHEN** a session is torn down while its encoder is being replaced
- **THEN** the teardown does not wait for the replacement, the replacing request fails with the
  session-evicted error, and the session's lease and directory are released

#### Scenario: Slow teardown at the transcode cap

- **WHEN** the transcode cap is full and a torn-down session's encoder is slow to exit
- **THEN** a new session is refused until that encoder has stopped, and the governor keeps
  counting it as a realtime transcode until then

#### Scenario: Shutdown during a replacement

- **WHEN** the server shuts down while a session's encoder is being replaced, or while another
  teardown is still stopping an encoder
- **THEN** shutdown returns only after those encoders have stopped and their leases and
  directories are released

#### Scenario: Preemption during a replacement

- **WHEN** the transcode cap is full and a higher-priority session asks for a slot while the only
  session holding one is replacing its encoder
- **THEN** the higher-priority session is refused rather than launched over the cap
