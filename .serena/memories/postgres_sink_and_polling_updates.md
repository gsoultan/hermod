### Postgres Sink & Polling Infrastructure Improvements

- **Fixed Postgres Sink Connection Hang**: Addressed a critical issue where testing a Postgres sink connection would hang the worker if the connection was slow or failing.
    - Implemented `singleflight` in `pkg/infra/pgxutil` to deduplicate concurrent connection attempts to the same DSN.
    - Modified `internal/engine/worker/health.go` to run resource health probes (like Postgres connection tests) in background goroutines, preventing them from blocking the main worker heartbeat loop.
- **Postgres Non-CDC Polling Support**: Enhanced `pkg/comm/source/postgres` to support polling-based data extraction for environments where logical replication (CDC) is unavailable (e.g., restricted permissions or certain PgBouncer configurations).
    - Added `query` and `poll_interval` configuration options.
    - Implemented watermark tracking (using an 'id' column by default) to ensure only new records are fetched.
- **UI Realtime Status Updates**: Improved the admin workers page (`ui/src/pages/admin/WorkersPage.tsx`) to provide realtime status feedback.
    - Integrated a 5-second auto-refetch interval using TanStack Query, ensuring worker online/offline status is accurately reflected without manual page refreshes.
- **PgBouncer Compatibility**: Verified system stability and performance when Postgres sources and sinks are placed behind PgBouncer.
    - Ensured connection strings correctly strip pooling markers before reaching the driver level.
    - Confirmed high-traffic stability (1000+ messages) with low resource consumption (~2MB memory overhead during peak).
- **Comprehensive E2E Testing**: Added new end-to-end test scenarios in `internal/engine/registry` (`e2e_postgresql_test.go` and `e2e_pgbouncer_real_test.go`) covering full Source -> Transformation -> Sink workflows with real Postgres databases.