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
2. **Backup first.** Before anything is downloaded, a SQLite metadata database is copied to
   `backups/` in the config folder (`/var/lib/hermod/backups` for a package install), named after
   the version it came from; the folder is private to the owner and keeps the newest three. The
   copy is consistent while Hermod keeps running, and stored credentials stay encrypted with the
   master key. When scheduled backups are configured (`backup.directory` in `config.yaml`), that
   backup is written too. If either fails, nothing is changed. Hermod cannot copy a PostgreSQL,
   MySQL or MongoDB metadata database; the install dialog says so, and asks you to back it up
   first. `hermod --update` copies a SQLite database the same way.
3. **Checksum.** The archive's SHA-256 must match the entry in the release's `checksums.txt`.
4. **It runs.** The extracted binary is run with `--version` and must report the release it was
   downloaded as. A binary for the wrong architecture, or a damaged one, stops here.
5. **Atomic swap.** The current binary is kept beside the new one as `hermod.previous`, and the
   new binary is renamed over the old one, so the file on disk is always one whole binary.
   A `.deb` install is put in place by `dpkg --install` instead (see below), which replaces each
   file the same way.
6. **Graceful restart.** Hermod shuts down the way it does on `SIGTERM` (flows drain, storage
   closes) and comes back as the new version. Under a service manager (systemd, launchd, Windows
   services) it exits with code 75 and the manager starts it again; started by hand, it replaces
   itself in place.
7. **Automatic rollback.** The new version must stay up for a minute to be confirmed. If it fails
   to get that far three starts running, the fourth start puts `hermod.previous` back and restarts
   into it, and the Updates page says so. Rollback restores the binary only: a database migration
   the new version ran is not undone, and an older version refuses to start on a schema newer
   than it knows. The rollback notice then names the copy from step 2: stop Hermod, put that copy
   in place of the database, and start it.

Running workflows stop the way they do on `systemctl stop`: each one stops reading and is given
up to 30 seconds to finish what it is processing, and stays marked active, so the new version
starts the same workflows again from the position they last acknowledged. A record that was read
but not acknowledged when a workflow stopped is read again (delivery is at least once), as after
any restart.

Until a release is confirmed, `hermod.update.json` beside the binary records the pending update;
`hermod.last-update.json` records how the last one ended.

## Installs from the `.deb`

When the binary is `/usr/bin/hermod` and dpkg says the `hermod` package owns it, Hermod installs
the release's `.deb` rather than the archive, so `dpkg -l hermod` keeps telling the truth and
`hermodctl` and `hermod-edge` are upgraded with it. Steps 1 to 4 and 6 to 7 are the same; in
between:

- the `.deb` is downloaded to the system's temporary folder and must match `checksums.txt`;
- `dpkg-deb` must report the package as `hermod`, and the `hermod` binary read from it
  (`dpkg-deb --fsys-tarfile`, without running `tar`) must report the release's version;
- `dpkg --force-confdef --force-confold --install` installs it without prompting and keeps any
  changed configuration. The package does not restart Hermod on an upgrade; Hermod restarts
  itself into the new version as in step 6.

This needs Hermod to run as root, which the packaged unit and `hermod --service install` both
do. `dpkg`, `dpkg-deb` and `dpkg-query` are run from `/usr/bin` (or `/bin`), never looked up on
`PATH`, and the package is downloaded into a fresh folder only root can read. If dpkg fails before it has replaced the binary, nothing changed and the page shows dpkg's
own message (for example, another `apt` holding the lock). A rollback restores
`/usr/bin/hermod.previous` over the binary, as for any install; dpkg then still lists the newer
version, so install the older `.deb` by hand if you want the two to agree.

A release whose build failed before it uploaded its files is not offered to an installation
that could install it, because there would be nothing to download.

### "Cannot mkdir: Function not implemented"

Up to v1.26.2 the packaged unit set `RestrictSUIDSGID=yes`. Its seccomp filter answers every
`openat2` call with `ENOSYS`, and the `tar` that `dpkg-deb --extract` runs needs `openat2`, so an
update from the Updates page stopped with `dpkg-deb could not unpack hermod_<version>_<os>_<arch>.deb
... Cannot mkdir: Function not implemented`. Nothing was changed. Later releases drop the setting
and no longer run `tar`, but the version that is running decides how the update runs, so lift
the filter once on a server still on one of those versions:

```sh
sudo mkdir -p /etc/systemd/system/hermod.service.d
printf '[Service]\nRestrictSUIDSGID=no\n' | sudo tee /etc/systemd/system/hermod.service.d/allow-update.conf
sudo systemctl daemon-reload
sudo systemctl restart hermod
```

Then install the update from the page again. The file can stay: it only repeats what later
releases already do.

## Where Hermod cannot replace itself

The page explains why and offers no Install button when:

| Installed as | Update it by |
| :--- | :--- |
| Container image or Helm chart | Pulling the new image tag or upgrading the chart |
| `.rpm` or `.apk` package (binary in `/usr/bin`) | Your package manager, with the package from the release page, or the [one-line installer](install.md#on-a-linux-server-in-one-command) again |
| `.deb` package, with Hermod running as a user other than root | `sudo apt install ./hermod_<version>_linux_<arch>.deb`, or the one-line installer again |
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
