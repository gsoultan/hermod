# Where the crypto master key comes from (and why setup must not pick its own)

- Start-up order (`cmd/hermod/main.go` initMasterKey / getStorageConfig):
  `--master-key` flag > `HERMOD_MASTER_KEY` > `crypto_master_key` in
  db_config.yaml. `LoadDBConfig` also overlays `HERMOD_MASTER_KEY` onto
  `cfg.CryptoMasterKey`. `config.PinnedMasterKey()` = flag (recorded by
  `config.SetPinnedMasterKey` in initMasterKey) else the env var; empty means
  the file decides. `config.FileMasterKey()` reads the file's own key, no env.
- Package installs (.deb/.rpm) always pin it: postinstall writes
  `HERMOD_MASTER_KEY` into `/etc/hermod/hermod.env`.
- Bug fixed (production "secret X of vhost Y cannot be decrypted with the
  current master key", surfaced as `config 'scheduled': reading secret ...`
  when starting a workflow): the setup wizard generated its own key
  (SetupPage.tsx), saved it to db_config.yaml and installed it in memory; the
  same for PUT /api/config/crypto rotation. Everything saved before the next
  restart (self-update restarts) was under that key, then the pinned key won.
- Now: `masterKeyForSetup` (infra transport http) makes both setup endpoints
  use the pinned key; rotation answers 409 via `pinnedKeyRefusal` when pinned;
  `/api/config/status` returns `master_key_pinned` and the wizard shows a note
  instead of the key field.
- Recovery at start-up (`cmd/hermod/master_key.go`): when the file key differs
  from the pinned key, `crypto.AddDecryptKey(fileKey)` (decrypt-only, kept
  across SetMasterKey), then `moveOffFileMasterKey` runs `ReEncryptSecrets`
  under the pinned key once (all-or-nothing), recorded in setting
  `crypto_file_key_migrated` (sha256 fingerprint of the key pair). The file key
  is kept: pre-update DB backups are still under it.
