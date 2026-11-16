# Installing Hermod

A Linux server in one command, the container image, the Helm chart, packages, building from
source, and `go get`.

The current release is **`v1.23.0`**. See [CHANGELOG.md](../CHANGELOG.md) for what
changed, what breaks, and the known gaps, and the
[releases page](https://github.com/gsoultan/hermod/releases) for every version.

**Pin an explicit version** for both the image and the chart. It is what makes a
deployment reproducible, and it is what the examples below do.

## Container image

```bash
docker pull ghcr.io/gsoultan/hermod:1.23.0
docker run --rm ghcr.io/gsoultan/hermod:1.23.0 -version
```

`linux/amd64` and `linux/arm64` are both published under that one tag.

## Helm

```bash
helm install hermod oci://ghcr.io/gsoultan/charts/hermod \
  --version 1.23.0 \
  --set existingSecret=hermod-master-key
```

The chart is versioned with the release. Production settings and the chart's values
are in [Deploying and running Hermod](deployment.md).

## On a Linux server, in one command

```bash
curl -fsSL https://raw.githubusercontent.com/gsoultan/hermod/main/deploy/package/install.sh | sudo sh
```

It picks the newest release's package for the machine (`.deb`, `.rpm` or `.apk`, `amd64` or
`arm64`), checks it against the release's `checksums.txt`, and installs it with the package
manager. Then open `http://<server>:4000` to finish setting Hermod up.
`HERMOD_VERSION=v1.25.0` before `sh` installs that release instead. Running it again upgrades.

## Packages

`.deb`, `.rpm`, `.apk` and tarballs for Linux `amd64`/`arm64` are attached to each
[release](https://github.com/gsoultan/hermod/releases). Installing a package is all a server
needs; on a first install it:

- installs `hermod`, `hermodctl` and `hermod-edge` into `/usr/bin`,
- installs the `hermod` systemd unit (standalone mode, port 4000, restarted when it stops),
- writes `/etc/hermod/hermod.env` (readable by root only) with `HERMOD_CONFIG_DIR=/var/lib/hermod`
  and a `HERMOD_MASTER_KEY` generated for that machine. **Back the key up**: the credentials
  Hermod stores cannot be read without it.
- and enables and starts the service.

```bash
sudo apt install ./hermod_1.26.0_linux_amd64.deb   # or: sudo dnf install ./hermod_1.26.0_linux_amd64.rpm
systemctl status hermod
journalctl -u hermod -f
```

Change settings (mode, port, database) in `/etc/hermod/hermod.env` and the unit's
`ExecStart`, via `systemctl edit hermod`, then `systemctl restart hermod`. An upgrade keeps
`hermod.env` and does not restart Hermod; removing the package stops the service and keeps
`/etc/hermod` and `/var/lib/hermod`.

If `hermod --service install` wrote a unit in `/etc/systemd/system/hermod.service` before the
package shipped its own, that unit keeps running Hermod and the package leaves it and its
settings alone.

A `.deb` install updates itself from the Updates page like a tarball install does; `.rpm` and
`.apk` installs are upgraded with the package manager. See [Updating Hermod](updating.md).

## From source

```bash
git clone https://github.com/gsoultan/hermod && cd hermod
go build ./cmd/hermod
```

Go 1.27 or later is required.

## `go get`

`go get github.com/gsoultan/hermod` and `go install github.com/gsoultan/hermod/cmd/hermod@latest`
resolve to the latest release.

Versions `v1.0.0` to `v1.8.0`, and `v1.0.0-rc.1`, are retracted in `go.mod`, so the Go
tooling never selects them. The module proxy is immutable and still serves them, and
`v1.0.0` in particular is bound to a commit from February whose `go.mod` declared
`github.com/user/hermod`, a path matching no repository. Nothing was lost by retracting
them: the module path was a placeholder until 2026-09-02, so none of those releases was
ever importable. Ask for a version above `v1.8.0`.
