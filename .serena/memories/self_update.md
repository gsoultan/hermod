# Self-update (Settings → Updates)

Hermod checks GitHub Releases of gsoultan/hermod and can replace its own binary.
User docs: `docs/updating.md`.

- `internal/selfupdate` decides what and when: `Canonical` (first word of
  `version.Version`, must be full semver, so `dev` builds are never offered
  anything), `Pick`, `Settings` (row `update_settings`: mode automatic|manual|off,
  default manual, interval 1..168h, include_prereleases), `Service` (Check,
  Install in the background, Run loop polling every 10 min against the interval).
  Automatic installs only within the same major, and never re-tries a tag that
  failed or was rolled back on this machine (`AutoInstallHeld`).
- `internal/selfupdate/install` does the work: assets only from
  `github.com/gsoultan/hermod/releases/download/` or `*.githubusercontent.com`
  (redirects too); SHA-256 against `checksums.txt`; extract top-level `hermod`
  from `hermod_<ver>_<os>_<arch>.tar.gz`; run it with `--version` and require the
  tag; hard-link the old one to `<exe>.previous`; write `<exe>.update.json`; rename
  over the exe. `Detect` refuses containers, rpm/apk installs in `/usr/bin`,
  non-linux/amd64|arm64, dev builds and unwritable folders.
- `.deb` installs (`/usr/bin/hermod` owned by package `hermod` per
  `dpkg-query --search`, process euid 0) get `Capability.Method = deb`:
  `Installer.installDeb` (`install/deb.go`) downloads `hermod_<ver>_<os>_<arch>.deb`
  to a temp dir, checks SHA-256, `dpkg-deb --field Package` must be `hermod`,
  `dpkg-deb --fsys-tarfile` read with Go's archive/tar (never GNU tar: see
  below) + `--version` smoke test, then `arm` (keep `.previous`,
  write marker) and `dpkg --force-confdef --force-confold --install`. If dpkg
  fails and `/usr/bin/hermod` is still the `.previous` inode, the marker is
  removed; otherwise it stays armed. Rollback is binary-only (dpkg db then lists
  the newer version). Non-root deb installs explain how to `sudo apt install`.
- `Pick` takes a `usable` filter: when this install can install, only releases
  carrying its asset (`Capability.AssetName`) and `checksums.txt` are offered
  (v1.25.1 and v1.26.0 were published with no files).
- Boot guard (`GuardBoot` in `cmd/hermod/main.go`, only for real starts, not
  `--service install` etc.): each start of the *marker's* version counts; the
  4th unconfirmed start restores `.previous`. A marker for another version is
  stale and dropped. `confirmUpdate` confirms after 1 minute up; only the version
  named in the marker can confirm. Result in `<exe>.last-update.json`.
- Restart: `install.RequestRestart` → signal handler drains like SIGTERM → after
  `service.Manage` returns, supervised processes exit 75 (EX_TEMPFAIL, so
  on-failure supervisors restart too), interactive ones `syscall.Exec` themselves.
- Routes `/api/system/updates{,/check,/install,/settings}` are admin-only with
  **no first-run bypass** (unlike `RbacMiddleware`); install/settings are audited.
- Before install, `selfupdate.PreUpdateBackup.Take` (built by
  `Server.PreUpdateBackup`, or `commandLineBackup` for `--update`) copies a
  SQLite metadata DB with `VACUUM INTO` (`sqlStorage.SnapshotTo`, `CanSnapshot`)
  to `<configdir>/backups/hermod-before-update-<from>-<ts>.db` (dir 0700, file
  0600, newest 3 kept), plus the app backup when `backup.directory` is set; any
  failure aborts. The path rides in `Plan.Backup` → marker → `Outcome.Backup`, so
  a rollback names it (an old binary refuses a newer schema:
  `ErrSchemaFromTheFuture`). `Status.BackupPlan` tells the UI what will happen.
- Not covered: remote workers (use `hermod --update` there), release signing
  (integrity is checksum-over-TLS; a signature would need a release key), and DB
  migrations are not rolled back with the binary.
- The guard only exists in binaries built with this feature, so updating *to* a
  pre-feature release (e.g. v1.24.9) leaves a marker nobody confirms. Harmless.

# Packages and one-line install

- `.goreleaser.yaml` nfpms ships `deploy/package/hermod.service` to
  `/usr/lib/systemd/system/` plus `postinstall.sh` / `preremove.sh`. First install
  (deb `configure` with empty $2, rpm `1`) writes `/etc/hermod/hermod.env`
  (HERMOD_CONFIG_DIR=/var/lib/hermod, generated HERMOD_MASTER_KEY, 0600) and
  `systemctl enable --now hermod` when systemd runs. Upgrades only daemon-reload,
  never restart (the self-updater restarts itself). If
  `/etc/systemd/system/hermod.service` exists (from `hermod --service install`)
  the scripts touch nothing. Unit runs as root (dpkg needs it), Restart=always.
- The unit must not set `RestrictSUIDSGID=yes` or `SystemCallFilter=`
  (`service_unit_test.go` guards it). systemd's RestrictSUIDSGID seccomp filter
  returns ENOSYS for every `openat2`, and GNU tar (run by `dpkg-deb --extract`)
  opens folders with openat2: v1.26.1 → v1.26.2 failed with "Cannot mkdir:
  Function not implemented". Servers on ≤ v1.26.2 need the drop-in
  `/etc/systemd/system/hermod.service.d/allow-update.conf` with
  `RestrictSUIDSGID=no` (docs/updating.md), since the running version's unit
  and updater decide how its update runs.
- `deploy/package/install.sh` (curl | sudo sh): picks the newest release that
  has this machine's .deb/.rpm/.apk (skips empty releases and `-rc`), checks
  checksums.txt, installs with apt/dnf/yum/rpm/apk, try-restarts on upgrade.
- Release workflow runs goreleaser with `--parallelism 1`: four parallel cold
  compiles of hermod/hermod-edge (both pull anthropic-sdk-go) OOM the runner.

# Setup address on a network install

- The API binds `:<port>` (all interfaces, `cmd/hermod/server_util.go`). The
  first-run log used to print `http://localhost:<port>/setup`; it now uses
  `localSetupURLs` (`cmd/hermod/setup_url.go`): non-loopback, non-link-local
  interface IPs (IPv4 first), then hostname, then localhost. Logged as
  `setup_url` (first) and `setup_urls`. A cloud VM's public IP is not on its
  interfaces; the usual "can't reach setup" cause on AWS is the security group
  not allowing the port.
