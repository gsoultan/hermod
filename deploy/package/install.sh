#!/bin/sh
# Installs Hermod on a Linux server from its GitHub release, as a service that
# starts on boot:
#
#   curl -fsSL https://raw.githubusercontent.com/gsoultan/hermod/main/deploy/package/install.sh | sudo sh
#
# It picks the package for this machine (.deb, .rpm or .apk), checks it
# against the release's checksums.txt, and installs it with the package
# manager. The package creates /etc/hermod/hermod.env with a master key of its
# own and starts Hermod on port 4000. Running it again upgrades to the newest
# release; the Updates page in Hermod does the same from then on.
#
# HERMOD_VERSION=v1.25.0 installs that release instead of the newest one.
set -eu

REPO=gsoultan/hermod
API="https://api.github.com/repos/$REPO/releases?per_page=30"

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = Linux ] || die "Hermod's packages are for Linux. On $(uname -s), build it from source or run the container image."
[ "$(id -u)" -eq 0 ] || die "run it as root: curl -fsSL https://raw.githubusercontent.com/$REPO/main/deploy/package/install.sh | sudo sh"

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "no release is built for $(uname -m); only amd64 and arm64 are." ;;
esac

if command -v dpkg >/dev/null 2>&1; then
  format=deb
elif command -v rpm >/dev/null 2>&1; then
  format=rpm
elif command -v apk >/dev/null 2>&1; then
  format=apk
else
  die "found none of dpkg, rpm or apk. Take the .tar.gz from https://github.com/$REPO/releases instead."
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q --https-only -O "$2" "$1"; }
else
  die "needs curl or wget."
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

# The release listing is newest first. Take the first release that actually
# carries this machine's package: a release whose build failed has no files,
# and a pre-release (a version with a "-") is skipped unless asked for.
fetch "$API" "$work/releases.json"
version=${HERMOD_VERSION:-}
version=${version#v}
if [ -n "$version" ]; then
  pattern="hermod_${version}_linux_${arch}\\.${format}"
else
  pattern="hermod_[0-9][0-9.]*_linux_${arch}\\.${format}"
fi
url=$(grep -o "\"browser_download_url\": *\"[^\"]*/${pattern}\"" "$work/releases.json" | head -n 1 | sed 's/.*"\(https[^"]*\)"$/\1/') || true
[ -n "$url" ] || die "no release has hermod_${version:-<version>}_linux_${arch}.${format}. See https://github.com/$REPO/releases."
file=${url##*/}
tag=${url%/*}
tag=${tag##*/}

say "Downloading Hermod $tag ($file)"
fetch "$url" "$work/$file"
fetch "${url%/*}/checksums.txt" "$work/checksums.txt"

want=$(awk -v f="$file" '$2 == f { print $1 }' "$work/checksums.txt")
[ -n "$want" ] || die "checksums.txt of $tag does not list $file."
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$work/$file" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$work/$file" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || die "$file does not match its published SHA-256 (got $got, published $want). Nothing was installed."
say "Checksum matches."

upgrade=false
command -v hermod >/dev/null 2>&1 && upgrade=true

case $format in
  deb)
    # apt resolves the package's dependencies; dpkg alone is enough without it.
    if command -v apt-get >/dev/null 2>&1; then
      DEBIAN_FRONTEND=noninteractive apt-get install -y "$work/$file"
    else
      dpkg --install "$work/$file"
    fi
    ;;
  rpm)
    if command -v dnf >/dev/null 2>&1; then
      dnf install -y "$work/$file"
    elif command -v yum >/dev/null 2>&1; then
      yum install -y "$work/$file"
    else
      rpm -U --replacepkgs "$work/$file"
    fi
    ;;
  apk)
    # The release's .apk is not signed with a key apk knows; the checksum
    # above is what vouches for it.
    apk add --allow-untrusted "$work/$file"
    ;;
esac

# The package does not restart a running Hermod on an upgrade (Hermod
# upgrading itself restarts on its own terms), so do it here.
if $upgrade && [ -d /run/systemd/system ]; then
  systemctl try-restart hermod.service || true
fi

say ""
say "Hermod $tag is installed."
if [ -d /run/systemd/system ]; then
  say "  Status:   systemctl status hermod"
  say "  Logs:     journalctl -u hermod -f"
  say "  Settings: /etc/hermod/hermod.env (then: systemctl restart hermod)"
  say "  Open http://$(hostname -f 2>/dev/null || hostname):4000 to finish setting it up."
else
  say "  This machine does not run systemd. Start it with:"
  say "    set -a; . /etc/hermod/hermod.env; set +a; hermod --mode=standalone"
fi
