#!/bin/sh
# Point apt at the Debian snapshot named by FFMPEG_DEBIAN_SNAPSHOT in
# internal/ffmpeg/ffmpeg.lock (CORE-2497), with source records, so every
# library the bundled ffmpeg links resolves to one exact version and its
# corresponding source can be fetched at that version.
#
# Usage: apt-snapshot.sh LOCK
set -eu

lock="$1"
snapshot="$(sed -n 's/^FFMPEG_DEBIAN_SNAPSHOT=//p' "$lock")"
case "$snapshot" in
	[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z) ;;
	*)
		echo "apt-snapshot: FFMPEG_DEBIAN_SNAPSHOT missing or malformed in $lock" >&2
		exit 1
		;;
esac

rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat >/etc/apt/sources.list.d/debian-snapshot.sources <<EOF
Types: deb deb-src
URIs: http://snapshot.debian.org/archive/debian/${snapshot}
Suites: bookworm bookworm-updates
Components: main non-free
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb deb-src
URIs: http://snapshot.debian.org/archive/debian-security/${snapshot}
Suites: bookworm-security
Components: main non-free
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
EOF
# A snapshot's Release files expire; the signatures still authenticate them.
cat >/etc/apt/apt.conf.d/80beamfall-snapshot <<EOF
Acquire::Check-Valid-Until "false";
Acquire::Retries "5";
EOF
