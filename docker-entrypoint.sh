#!/bin/sh
set -eu
if [ "$(id -u)" = 0 ]; then
  /usr/local/bin/data-init
  exec su-exec 10001:10001 /usr/local/bin/pocket-relay "$@"
fi
# An explicitly supplied Compose user is respected; its data must be writable.
exec /usr/local/bin/pocket-relay "$@"
