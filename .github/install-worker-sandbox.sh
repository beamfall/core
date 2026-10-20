#!/bin/sh
# Installs the Linux Integration Worker OS sandbox (bubblewrap) on a CI runner.
# internal/integrationhost fails closed without bwrap on PATH, so the gate and
# race lanes need it. The version is pinned; apt verifies it against the signed
# archive index. Ubuntu 24.04 may restrict unprivileged user namespaces through
# AppArmor and ships no bwrap profile, so grant userns to /usr/bin/bwrap only.
set -eu

version=0.9.0-1ubuntu0.3

sudo apt-get update -qq
sudo apt-get install -y --no-install-recommends "bubblewrap=$version"
installed=$(dpkg-query -W -f '${Version}' bubblewrap)
if [ "$installed" != "$version" ]; then
	echo "bubblewrap $installed installed, want $version" >&2
	exit 1
fi

probe() {
	bwrap --die-with-parent --new-session --unshare-all --ro-bind / / true
}

if probe; then
	exit 0
fi

restrict=/proc/sys/kernel/apparmor_restrict_unprivileged_userns
if [ -r "$restrict" ] && [ "$(cat "$restrict")" = 1 ]; then
	sudo tee /etc/apparmor.d/bwrap >/dev/null <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>

profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE
	sudo apparmor_parser -r /etc/apparmor.d/bwrap || true
	if ! probe; then
		sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
	fi
fi

if ! probe; then
	echo "bwrap cannot create its sandbox on this runner" >&2
	exit 1
fi
