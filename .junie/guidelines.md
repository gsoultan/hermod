### Junie Guidelines - Hermod Project

#### General Principles
- **Keep it Simple**: Favor clear, readable code over clever or complex solutions.
- **Consistency**: Follow existing patterns and naming conventions in the project.
- **Small Commits**: Aim for small, logical changes that are easy to review.
- **Avoid Bloat**: Keep implementations lean—avoid unnecessary abstractions, layers, and dependencies. Remove dead/unused code and prefer small, focused modules.

#### Go Specific Guidelines
- **Standard Formatting**: Always use `gofmt` or `goimports` to format Go code.
- **Error Handling**: Explicitly handle all errors. Do not ignore them unless there's a very good reason.
- **Testing**:
    - Write unit tests for new logic.
    - Use the standard `testing` package.
    - Table-driven tests are preferred for multiple test cases.
- **Documentation**: Use standard Go doc comments for exported symbols.

#### UI/Frontend Guidelines
- **Type-Only Imports**: Use `import type` or `import { type X }` for TypeScript types and interfaces to comply with `verbatimModuleSyntax`.
- **Build Verification**: Ensure the UI builds successfully using `bun run build` in the `ui` directory after making changes.
- **Prioritize Lazy Loading**: Always use `React.lazy` and `Suspense` for route-level components and heavy/non-critical components. Lazy loading should be the default approach for code splitting to reduce initial bundle size and improve load times.

##### React Best Practices
- **Project & Types**: Use React with TypeScript and enable strict mode. Prefer precise types over `any`; export reusable prop types.
- **Component Design**: Favor small, focused function components. Keep components pure; avoid side-effects in render. Extract shared pure helpers to `utils`.
- **Props & APIs**: Accept minimal, explicit props. Use clear names; avoid prop drilling by lifting state or using Context sparingly.
- **State Management**: Start local (`useState`/`useReducer`). Introduce Context only for cross-cutting concerns (theme/auth). For server state, prefer a library like `@tanstack/react-query` over ad-hoc effects (caching, retries, deduping).
- **Hooks**: Follow the Rules of Hooks. Keep dependencies arrays accurate; do not suppress lint rules without justification. Memoize expensive computations with `useMemo` and stable callbacks with `useCallback` only when profiling shows benefit.
- **Data Fetching & Effects**: Keep effects minimal and idempotent. Abort in-flight requests on unmount/param changes (`AbortController`). Handle errors clearly; don’t swallow errors. Avoid `useEffect` for data fetching when using `@tanstack/react-query`; rely on `useQuery` for caching, retries, background refetching, and invalidation.
- **Performance**: Split heavy routes/components with `React.lazy`/`Suspense`. Virtualize long lists. Use stable `key` props (avoid array indices for dynamic lists). Avoid unnecessary re-renders by keeping state minimal and derived values computed on render.
- **Forms**: Use controlled inputs for simple forms; for complex forms, use TanStack Form (`@tanstack/react-form`) for schema-friendly validation, performance, and ergonomics.
- **Styling**: Be consistent (CSS Modules, Tailwind, or chosen CSS-in-JS). Co-locate styles; avoid deep selectors. Prefer design tokens/utility classes.
- **Accessibility (a11y)**: Prefer semantic HTML. Ensure keyboard operability and focus management. Provide labels, `aria-*` as needed. Use `eslint-plugin-jsx-a11y` and test with keyboard/screen readers.
- **Error Boundaries**: Wrap pages/critical trees with an Error Boundary to catch render-time errors and display a fallback UI.
- **Routing**: Co-locate route components and loader logic. Handle 404/unauthorized states. Preload critical data on navigation when useful.
- **Security**: Never render untrusted HTML (`dangerouslySetInnerHTML`) without thorough sanitization. Escape/encode user input. Keep secrets server-side; prefer HttpOnly cookies or secure storage strategies. Use CSP and avoid inline scripts/styles when possible.
- **Testing**: Test behavior with `@testing-library/react`; mock network with MSW. Prefer table-driven tests for hooks/utilities. Focus on user-observable outcomes over implementation details.
- **Code Quality & DX**: Keep components under one screenful. Delete dead code; avoid over-abstraction. Enforce formatting/linting; keep type-only imports.
- **Common Pitfalls**: Don’t store derived data in state; avoid effects for synchronous derivations; never mutate state/props; ensure dependency arrays are correct.
- **Rules of React**: Follow the standard Rules of React ([Rules of React](https://react.dev/learn/rules-of-react)). Use the React Compiler ([React Compiler](https://react.dev/learn/react-compiler)) if necessary for performance optimizations and automatic memoization.

#### Sink UI Guidelines
- **Interactive Configuration**: In the Workflow Editor, the Sink UI uses a 3-column layout:
    - **Source Data**: Explore available upstream fields and copy them as Go template variables (e.g., `{{.field_name}}`).
    - **Configuration**: Unified form for settings, dynamic parameters, and reliability policies.
    - **Setup Guide & Status**: Real-time test results and per-sink documentation.
- **Dynamic Parameters**: Use Go template syntax `{{.field}}` or `{{.metadata.key}}` in supported fields (e.g., SMTP Subject, To, SQL queries).
- **Go Templates & Loops**:
    - Iterate over arrays in your data using `range`:
      ```html
      {{range .items}}
        - {{.name}}: {{.value}}
      {{end}}
      ```
    - Use `{{if .condition}}...{{else}}...{{end}}` for conditional logic.
- **SMTP Dynamic Recipients**: The "To" field accepts comma-separated templates. If a variable contains multiple emails, they are automatically handled.

#### Architecture & Reliability
- **State Persistence**: The database is the single "Source of Truth" for workflow configurations and states.
- **Worker Reconciliation**:
    - On startup, workers must perform an **Initial Sync** to resume any workflows marked as `Active` in the database.
    - Workers should run a continuous **background loop** (reconciliation engine) to detect new, updated, or crashed workflows and align the running engine state with the database.
- **Graceful Shutdown & Data Safety**:
    - Implement graceful shutdowns by draining internal message buffers before the process exits.
    - Ensure messages are only acknowledged (`Ack`) from the source **after** they have been successfully written to all configured sinks.
- **DLQ Prioritization**: 
    - When `PrioritizeDLQ` is enabled, the engine uses a `PrioritySource` to drain the Dead Letter Sink before reading from the primary source.
    - **Requirement**: The Sink assigned as a Dead Letter Sink must also implement the `hermod.Source` interface (e.g., Postgres, MySQL, NATS, Kafka).
    - **Idempotency**: Always enable idempotency on primary sinks when using DLQ prioritization to avoid side effects from re-processed messages.
- **Distributed Coordination**:
    - When running multiple worker instances, use **hash-based sharding** (e.g., FNV-1a) or **explicit GUID assignments** to ensure that each workflow is processed by exactly one worker instance at a time.

#### Backend Guidelines
- **Code Cleanliness**: Prefer simple, composable functions and minimal dependencies. Remove dead code and keep packages narrowly focused on a single concern.
- **SOLID Design**:
    - Single Responsibility: one reason to change per module/function.
    - Open/Closed: extend behavior via new types/impls; avoid modifying stable code.
    - Liskov Substitution: interfaces should allow safe substitution of implementations.
    - Interface Segregation: prefer small, focused interfaces.
    - Dependency Inversion: depend on abstractions (interfaces), not concretions.
- **Programming by Interface**: Accept and return interfaces where appropriate; keep interfaces small and defined close to their consumers; provide constructors that accept interfaces for dependencies.
- **KISS (Keep It Simple, Stupid)**: Choose the simplest approach that works. Avoid premature abstraction and over-engineering.
- **DRY (Don't Repeat Yourself)**: Consolidate duplicated logic. Extract shared helpers with clear ownership and avoid copy-paste across packages.
- **Robustness and Error Handling**: Validate inputs; fail fast with clear messages; wrap errors with context using `%w`; avoid panics in library code; prefer sentinel errors or `errors.Is/As`; implement timeouts, cancellation via `context.Context`, and retries with backoff when appropriate.
- **Security Best Practices**: Do not log secrets or PII; use parameterized queries for all SQL; validate and sanitize external inputs; use least-privilege credentials; enable TLS/secure defaults; keep dependencies updated and pinned.
- **Refactoring and Small Functions**: Keep functions small and focused (aim for one screenful or less). Extract pure helpers to reduce complexity and improve testability. Refactor incrementally with tests.
- **Readable and Consistent Code**: Follow existing naming and package structure. Run `gofmt`/`goimports`. Keep cyclomatic complexity low. Prefer explicitness over cleverness.
- **Documentation and Comments**: Write Go doc comments for exported symbols and packages; explain the "why" behind non-obvious decisions; include examples for important public APIs.
- **AI Code Agent Friendly**: Favor deterministic, explicit code and clear invariants; avoid hidden side effects; use structured logs (key/value) to aid troubleshooting; keep build/run instructions up to date in the README.

##### Object-Oriented Programming
- **Composition over Inheritance**: Prefer embedding structs (`struct { Field EmbeddedType }`) for "has-a" relationships and code reuse instead of inheritance.
- **Interfaces for Polymorphism**: Define small interfaces for behaviors; types satisfy them implicitly (duck typing).
- **Encapsulation**: Use unexported (lowercase) fields with exported getter/setter methods.
- **Interface Design**: Define interfaces close to consumers; 1-3 methods ideally.
- **Pointer Receivers**: Use `*Type` for methods that mutate; `Type` for read-only.

#### Workflow
- Always check for existing tests before making changes.
- Ensure the project compiles after every change (both Go and UI).
- Use `go run cmd/hermod/main.go --build-ui` to verify the full build and integration.
- Update the README if any significant features are added or changed.

- Lightweight verification (Windows): Prefer targeted, fast checks over repo-wide builds/tests.
  - Use the helper script `scripts/quick-verify.ps1` to build the main binary and run focused unit tests only (avoids `go test ./...` and `go build ./...`).
    - Quick run: `pwsh -File scripts/quick-verify.ps1`
    - Include integration tests explicitly: `pwsh -File scripts/quick-verify.ps1 -All`
      - Set required env vars as needed (e.g., `HERMOD_INTEGRATION=1`, `REDIS_ADDR`, `MYSQL_DSN`).
