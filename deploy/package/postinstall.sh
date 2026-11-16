#!/bin/sh
# Runs after the hermod package is unpacked: on a first install it creates the
# settings file, with a master key of its own, and starts the service. An
# upgrade changes nothing but systemd's view of the unit file, and does not
# restart Hermod: an upgrade from the Updates page restarts it itself, and one
# by hand is restarted by whoever ran it.
set -e

ENV_FILE=/etc/hermod/hermod.env
DATA_DIR=/var/lib/hermod

# deb passes "configure" and the version it upgrades from, empty on a first
# install; rpm passes 1 on a first install and 2 on an upgrade. Alpine has no
# systemd, so there is nothing to start there.
first_install=false
case "$1" in
  configure) [ -z "$2" ] && first_install=true ;;
  1) first_install=true ;;
esac

# A unit someone wrote with `hermod --service install` before the package
# shipped one is theirs: it keeps its own settings, so none are made here.
own_unit=false
[ -e /etc/systemd/system/hermod.service ] && own_unit=true

write_settings() {
  mkdir -p "$DATA_DIR" "$(dirname "$ENV_FILE")"
  chmod 0750 "$DATA_DIR"
  [ -e "$ENV_FILE" ] && return 0
  umask 077
  key=$(head -c 32 /dev/urandom | base64 | tr -d '\n')
  cat > "$ENV_FILE" <<ENV
# Hermod settings, read by hermod.service. Change them, then run:
#   systemctl restart hermod
#
# Where Hermod keeps its configuration, the SQLite database it starts with,
# and its worker identity.
HERMOD_CONFIG_DIR=$DATA_DIR
# Encrypts the credentials Hermod stores. Back it up: without it they cannot
# be read. Generated for this machine when the package was installed.
HERMOD_MASTER_KEY=$key
ENV
  chmod 0600 "$ENV_FILE"
}

$own_unit || write_settings

# Only where systemd is running: not in a container build or on Alpine.
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  if $first_install && ! $own_unit; then
    systemctl enable --now hermod.service || true
    echo "Hermod is running. Open http://$(hostname -f 2>/dev/null || hostname):4000 to finish setting it up."
  fi
fi
exit 0
