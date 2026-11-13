# Keep encoder teardown and spawn off the session manager's global lock

Ticket: CORE-1348 (`queue:core:main`, PLAYBACK-SESSIONS-LOCK-SCOPE-1).

## Why

The live HLS session manager serializes every session behind one mutex. Two paths do slow work
while holding it:

- Replacing an encoder for a far seek or a rendition switch stops the old FFmpeg process, which
  waits for it to exit, and then spawns the new one.
- Removing a session (idle reap, preemption, device revocation, shutdown, or a change of
  mode-shaping parameters) stops the process and deletes the session's directory.

While any one of these runs, segment requests for every other session wait. One encoder that
is slow to exit stalls playback server-wide.

## What Changes

- Removing a session takes it out of the map under the lock. The caller then stops the process
  and deletes the directory after releasing the lock. The session's slot and transcode lease stay
  held until its process has stopped, so the transcode cap and the governor still see it running.
- Replacing or starting an encoder marks the session as starting, then releases the lock to
  stop the old process, tear down any preempted victim and spawn the new process. A per-session
  start mutex, always taken before the global lock, stops a second request on the same session
  from starting another encoder meanwhile.
- A session that is starting still counts against the transcode cap and keeps its lease.
- If the session is evicted while the lock is released, the start stops what it spawned,
  removes the directory again, releases the lease and slot, and fails with the session-evicted
  error.
- Shutdown waits for starts running with the lock released, so no encoder outlives it.
