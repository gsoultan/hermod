# Setup wizard: what holds the Next button

`ui/src/pages/system/SetupPage.tsx` is the first-run wizard at `/setup`.

- **Database step:** Next needs a database type, a connection string/path, a
  crypto master key of 16+ characters, the logging DB fields when "separate
  logging database" is ticked, **and a passing Test Connection**. Any change to
  type, connection or key clears the test result, so it must be run again.
- **Admin step:** Next needs a username and a password scoring 3+ on
  `passwordScore` (8+ chars, lowercase, uppercase, digit, symbol: one point each).
- `POST /api/config/database/test` (`internal/infra/transport/http/infra.go`,
  `TestDBConfig`) answers **200 with `{ok:false, error}`** for a database it
  cannot reach; only a malformed body or unknown type is a 4xx. The UI must show
  the error from `testResult` whenever `ok` is false, not only on a mutation
  error. Before this was fixed, a failed test showed nothing and Next simply
  stayed disabled, which read as a broken installer.
- The wizard now prints the first unmet condition above the Next button
  (`nextBlocker`). Keep that list in step with `canDbNext` / `canAdminNext`.
- Linux service installs run with the binary's folder as working directory, so a
  relative SQLite path (`hermod.db`) fails there when that folder is not
  writable; an absolute writable path such as `/var/lib/hermod/hermod.db` works.

Tests: `ui/src/__tests__/setupWizardNext.test.tsx`.
