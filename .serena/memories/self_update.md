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
  over the exe. `Detect` refuses containers, `/usr/bin` (package installs),
  non-linux/amd64|arm64, dev builds and unwritable folders.
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
- Before install, `Server.BackupBeforeUpdate` writes a backup when
  `backup.directory` is configured; a failed backup aborts.
- Not covered: remote workers (use `hermod --update` there), release signing
  (integrity is checksum-over-TLS; a signature would need a release key), and DB
  migrations are not rolled back with the binary.
- The guard only exists in binaries built with this feature, so updating *to* a
  pre-feature release (e.g. v1.24.9) leaves a marker nobody confirms. Harmless.
