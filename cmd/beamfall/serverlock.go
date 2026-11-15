package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/restorestage"
)

// serverLockName is the file a running `serve` holds an exclusive advisory
// lock on for the life of the process. The lock, not the file, is the signal:
// the OS drops it when the process exits or crashes, so a leftover file never
// blocks offline commands.
const serverLockName = "server.lock"

// errServerLockHeld reports that another process holds the data directory's
// server lock.
var errServerLockHeld = errors.New("server lock held by another process")

// acquireServerLock creates <dataDir>/server.lock (0600) and takes an
// exclusive, non-blocking lock on it. The caller must call release once it no
// longer needs exclusive ownership of the data directory.
func acquireServerLock(dataDir string) (release func(), err error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil { // #nosec G703 -- operator-chosen data directory (flag or env)
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, serverLockName), os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 G703 -- operator-chosen data directory; fixed file name
	if err != nil {
		return nil, err
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// lockServeDataDir takes the server lock for `serve`, naming the data
// directory when another server already owns it. It refuses while a staged
// restore journal exists, both before locking (so it does not create a data
// directory a restore has renamed away) and after (a restore that renamed the
// directory after the first check moved its lock with it, so only the journal
// shows it is running).
func lockServeDataDir(dataDir string) (release func(), err error) {
	if err := refuseInterruptedRestore(dataDir); err != nil {
		return nil, err
	}
	release, err = acquireServerLock(dataDir)
	if errors.Is(err, errServerLockHeld) {
		return nil, fmt.Errorf("another Beamfall server is already running against data directory %s", dataDir)
	}
	if err != nil {
		return nil, err
	}
	if err := refuseInterruptedRestore(dataDir); err != nil {
		release()
		return nil, err
	}
	// A start that stopped after claiming a staged set but before renaming
	// the data directory leaves the decrypted set beside it; the data is
	// intact, so remove the copy.
	restorestage.RemoveOrphanClaims(dataDir)
	return release, nil
}

// refuseServerLock fails when a running server holds the data directory's
// server lock. It only probes: the lock is released again before returning,
// so offline commands may rename or replace the data directory afterwards.
func refuseServerLock(dataDir string) error {
	f, err := os.OpenFile(filepath.Join(dataDir, serverLockName), os.O_RDWR, 0) // #nosec G304 G703 -- operator-chosen data directory; fixed file name
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFileExclusive(f); err != nil {
		if errors.Is(err, errServerLockHeld) {
			return errors.New("server appears to be running; stop the server before bootstrapping the first API token")
		}
		return err
	}
	return unlockFile(f)
}
