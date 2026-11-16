#!/bin/sh
# Stops the packaged service when the package is removed, not when it is
# upgraded, and removes the files Hermod's updates left in /usr/bin. Settings
# and data in /etc/hermod and /var/lib/hermod are kept.
set -e

# deb passes "remove" (an upgrade is "upgrade"); rpm passes 0 when the
# package is going away and 1 on an upgrade.
case "$1" in
  remove|0) ;;
  *) exit 0 ;;
esac

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 \
  && [ ! -e /etc/systemd/system/hermod.service ]; then
  systemctl disable --now hermod.service || true
fi

# What Hermod's own updates leave beside the binary: the previous version kept
# for rollback and the update records. They belong to no package.
rm -f /usr/bin/hermod.previous /usr/bin/hermod.update.json /usr/bin/hermod.last-update.json
exit 0
