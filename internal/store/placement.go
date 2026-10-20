package store

import (
	"fmt"
	"strings"
)

var detectDataDirFilesystem = platformDataDirFilesystem

type dataDirFilesystem struct {
	Name    string
	Magic   int64
	Known   bool
	Network bool
}

func guardDataDirPlacement(dataDir string, allowNetwork bool) error {
	fs, err := detectDataDirFilesystem(dataDir)
	if err != nil {
		return err
	}
	if fs.Network && !allowNetwork {
		return fmt.Errorf("data-dir %q is on network filesystem %q; move the Beamfall data directory to local storage or pass --allow-network-data-dir / BEAMFALL_ALLOW_NETWORK_DATA_DIR=1 if you accept SQLite corruption risk", dataDir, fs.Name)
	}
	return nil
}

func classifyDataDirFilesystem(name string, magic int64) dataDirFilesystem {
	normalized := strings.ToLower(strings.TrimSpace(name))
	fs := dataDirFilesystem{Name: normalized, Magic: magic, Known: normalized != ""}
	switch normalized {
	case "nfs", "nfs4", "smbfs", "smb", "cifs", "webdav", "webdavfs", "afpfs":
		fs.Network = true
		return fs
	}

	switch magic {
	case 0x6969, 0x517B, 0xFE534D42, 0xFF534D42:
		fs.Known = true
		fs.Network = true
		if fs.Name == "" {
			fs.Name = fmt.Sprintf("0x%x", magic)
		}
	}
	return fs
}
