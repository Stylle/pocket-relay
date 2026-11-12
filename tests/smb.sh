#!/usr/bin/env bash
set -euo pipefail
# Isolated loopback SMB server, no real NAS or credentials.
fixture=$(mktemp -d /tmp/pocket-relay-smb.XXXXXX)
trap 'test ! -f "$fixture/pid/smbd.pid" || sudo kill "$(cat "$fixture/pid/smbd.pid")" || true; sudo rm -rf "$fixture"' EXIT
chmod 755 "$fixture"
mkdir -p "$fixture/share" "$fixture/pid" "$fixture/lock" "$fixture/state" "$fixture/cache" "$fixture/private"
chmod 777 "$fixture/share"
cat > "$fixture/smb.conf" <<EOF
[global]
server role = standalone server
security = user
interfaces = 127.0.0.1
bind interfaces only = yes
smb ports = 445
pid directory = $fixture/pid
lock directory = $fixture/lock
state directory = $fixture/state
cache directory = $fixture/cache
private dir = $fixture/private
log file = $fixture/smb.log
[relay]
path = $fixture/share
guest ok = no
read only = no
force user = $(id -un)
EOF
printf 'fixture-password\nfixture-password\n' | sudo smbpasswd --configfile="$fixture/smb.conf" -s -a "$(id -un)"
sudo smbd --configfile="$fixture/smb.conf" --daemon
for n in $(seq 1 20); do if timeout 1 bash -c '</dev/tcp/127.0.0.1/445' 2>/dev/null; then break; fi; sleep 1; done
export POCKET_SMB_TEST=1
export POCKET_SMB_USER="$(id -un)"
export POCKET_SMB_PASSWORD=fixture-password
# setup-go resides in the invoking runner's PATH; sudo preserves it explicitly.
GO_BIN=$(command -v go || find /opt/hostedtoolcache/go -path '*/bin/go' | sort -V | tail -n1)
"$GO_BIN" test -timeout 90s -run TestSMBIntegration -v ./...
