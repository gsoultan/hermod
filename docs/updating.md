# Updating Hermod

Hermod can check for new releases and install them itself. An administrator manages this
on **Settings → Updates** (`/updates`), which is also linked from **About Hermod**.

## What the page offers

- **Check now** asks GitHub for the newest release of
  [gsoultan/hermod](https://github.com/gsoultan/hermod/releases) and says whether it is newer
  than the running version.
- **Install** downloads that release, verifies it, and restarts Hermod into it.
- **How updates are installed**:
  - **Automatic**: checks on the schedule below and installs a newer release by itself, then
    restarts. It only installs within the running major version (from v1.24 to v1.25, not to
    v2.0); a new major version is shown and waits for someone to install it. A version that was
    rolled back on this machine, or failed to install, is not retried automatically.
  - **Manual** (the default): checks on the schedule and shows the release here; installing it
    is a click.
  - **Off**: never checks on its own. Check now still works.
- How often to check (every 6 or 12 hours, daily, or weekly), and whether to include
  pre-releases (release candidates).

The setting is stored in the database (`settings` row `update_settings`), so every API replica
sharing the database follows it. Each replica updates its own binary. Changing the setting and
starting an install are written to the audit log (`UPDATE_SETTINGS`, `UPDATE_INSTALL`).

Checking sends one anonymous request to `api.github.com`; nothing about the installation is sent.
GitHub allows 60 such requests an hour from one address.

## How an install stays safe

1. **Only a published, newer release.** The release list is read again when an install starts;
   a version that is not newer, is a draft, or is a pre-release when pre-releases are off is
   refused. Downloads come only from `https://github.com/gsoultan/hermod/releases/download/...`
   and GitHub's asset storage on `*.githubusercontent.com`, redirects included.
2. **Backup first.** When scheduled backups are configured (`backup.directory` in `config.yaml`),
   a backup is written before anything is downloaded. If it fails, nothing is changed.
3. **Checksum.** The archive's SHA-256 must match the entry in the release's `checksums.txt`.
4. **It runs.** The extracted binary is run with `--version` and must report the release it was
   downloaded as. A binary for the wrong architecture, or a damaged one, stops here.
5. **Atomic swap.** The current binary is kept beside the new one as `hermod.previous`, and the
   new binary is renamed over the old one, so the file on disk is always one whole binary.
6. **Graceful restart.** Hermod shuts down the way it does on `SIGTERM` (flows drain, storage
   closes) and comes back as the new version. Under a service manager (systemd, launchd, Windows
   services) it exits with code 75 and the manager starts it again; started by hand, it replaces
   itself in place.
7. **Automatic rollback.** The new version must stay up for a minute to be confirmed. If it fails
   to get that far three starts running, the fourth start puts `hermod.previous` back and restarts
   into it, and the Updates page says so. Rollback restores the binary only: a database migration
   the new version ran is not undone.

Until a release is confirmed, `hermod.update.json` beside the binary records the pending update;
`hermod.last-update.json` records how the last one ended.

## Where Hermod cannot replace itself

The page explains why and offers no Install button when:

| Installed as | Update it by |
| :--- | :--- |
| Container image or Helm chart | Pulling the new image tag or upgrading the chart |
| `.deb`, `.rpm` or `.apk` package (binary in `/usr/bin`) | Your package manager, with the package from the release page |
| A binary in a folder the Hermod process cannot write to | Running Hermod as a user who owns that folder, or replacing the binary by hand |
| A development build, or a platform with no published build (only Linux `amd64`/`arm64` are published) | Building from source |

Checking still works in every case.

## From the command line

For remote workers, which have no Updates page, or a platform that will not start:

```bash
hermod --check-update   # is a newer release published?
hermod --update         # download, verify and install it; then restart the service
hermod --rollback       # put back the binary the last update replaced
```

`--update` does not restart anything itself. Restart with `hermod --service restart` or
`systemctl restart hermod`; the boot guard above applies to that start as well.

Workers are not updated by the platform. Keep them on the same version as the platform by running
`hermod --update` on each.
