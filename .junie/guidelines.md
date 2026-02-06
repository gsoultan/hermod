### Junie Guidelines - Hermod Project

#### General Principles
- **Keep it Simple**: Favor clear, readable code over clever or complex solutions.
- **Consistency**: Follow existing patterns and naming conventions in the project.
- **Small Commits**: Aim for small, logical changes that are easy to review.
- **Avoid Bloat**: Keep implementations lean—avoid unnecessary abstractions, layers, and dependencies. Remove dead/unused code and prefer small, focused modules.
- **Clean Code Patterns**:
    - **Avoid Deep Nesting**: Use guard clauses and early returns to handle edge cases and errors first. This keeps the "happy path" at the lowest indentation level and improves readability.
    - **Minimize Cyclomatic Complexity**: Keep functions small and focused. If a block of code has too many branches or nested loops, refactor it into smaller, named functions.
    - **Descriptive Naming**: Use clear, intention-revealing names for variables, functions, and types. Avoid cryptic abbreviations.
    - **Avoid Magic Values**: Use named constants instead of literal numbers or strings (magic values) to improve maintainability and clarity.

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
 - **No .mjs Files**: Do not add or commit `.mjs` files in the UI. Use `.ts`, `.tsx`, or `.js` as appropriate to keep tooling and bundler resolution consistent across the project.

##### React Best Practices
- **Eliminating Waterfalls (Critical)**:
    - **Defer Await**: Move `await` operations into the branches where they are actually used to avoid blocking code paths that don't need them.
    - **Parallelize**: Use `Promise.all()` for independent operations. Start independent promises early even if you don't await them immediately.
    - **Suspense Boundaries**: Use strategic `Suspense` boundaries to allow parts of the UI to render while others are still loading.
- **Bundle Size Optimization (Critical)**:
    - **Dynamic Imports**: Use `React.lazy` for heavy components, routes, and non-critical third-party libraries.
- **Re-render Optimization (High)**:
    - **Derived State**: Calculate derived state during rendering. Do not store it in state or use `useEffect` to sync it.
    - **Defer State Reads**: Don't subscribe to dynamic state (e.g., search params, store values) if you only read it inside event handlers or callbacks. Read them on demand.
    - **Primitive useMemo**: Do not wrap simple expressions with primitive result types (boolean, number, string) in `useMemo`; the overhead often exceeds the benefit.
    - **Stable Defaults**: Extract default non-primitive parameter values (arrays, objects) to constants outside the component to preserve memoization.
    - **Functional Updates**: Use functional updates in `setState` (e.g., `setCount(c => c + 1)`) to avoid unnecessary effect or callback dependencies.
- **Rendering Performance (Medium)**:
    - **useTransition**: Use `useTransition` for non-urgent updates (like filtering a list) to keep the UI responsive.
    - **Explicit Conditionals**: Use explicit boolean checks for conditional rendering (e.g., `count > 0 && ...`) instead of relying on truthiness of numbers (e.g., `count && ...`) to avoid rendering `0`.
    - **Ref for Transients**: Use `useRef` for values that don't trigger re-renders (timers, previous values, instance variables).
- **Project & Types**: Use React with TypeScript and enable strict mode. Prefer precise types over `any`; export reusable prop types.
- **Component Design**: Favor small, focused function components. Keep components pure; avoid side-effects in render. Extract shared pure helpers to `utils`.
- **Props & APIs**: Accept minimal, explicit props. Use clear names; avoid prop drilling by lifting state or using Context sparingly.
- **State Management**: Start local (`useState`/`useReducer`). Use `Zustand` for global client state. For server state, prefer `@tanstack/react-query` (caching, retries, deduping).
- **Hooks**: Follow the Rules of Hooks. Keep dependencies arrays accurate; do not suppress lint rules without justification. 
- **Data Fetching**: Keep effects minimal and idempotent. Abort in-flight requests on unmount/param changes (`AbortController`). Use `useQuery` for all data fetching.
- **Performance**: Virtualize long lists. Use stable `key` props (avoid array indices for dynamic lists).
- **Forms**: Use controlled inputs for simple forms; for complex forms, use TanStack Form (`@tanstack/react-form`).
- **Styling**: Be consistent with Mantine and Tailwind. Co-locate styles; prefer design tokens/utility classes.
- **Accessibility (a11y)**: Prefer semantic HTML. Ensure keyboard operability and focus management. Use `eslint-plugin-jsx-a11y`.
- **Error Boundaries**: Wrap pages/critical trees with an Error Boundary to catch render-time errors.
- **Routing**: Use `@tanstack/react-router`. Co-locate route components and loader logic.
- **Security**: Never render untrusted HTML without sanitization. Escape user input. Keep secrets server-side.
- **Testing**: Test behavior with `@testing-library/react`; mock network with MSW. Prefer table-driven tests for hooks/utilities.
- **Code Quality & DX**: Keep components under one screenful. Delete dead code; avoid over-abstraction. Enforce formatting/linting; keep type-only imports.
- **Common Pitfalls**: Don’t store derived data in state; avoid effects for synchronous derivations; never mutate state/props.
- **Rules of React**: Follow the standard Rules of React. Use the React Compiler if applicable for automatic optimizations.

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
- **SQL Management & Portability**:
    - Separate SQL queries from Go logic by moving them to a dedicated `queries.go` file within the same package.
    - Use a `queryRegistry` or similar pattern to manage common queries and driver-specific overrides.
    - Ensure all queries are compatible with supported database providers (SQLite, MySQL, Postgres, SQL Server).
    - Use driver-neutral placeholders (e.g., `?`) and implement a translation layer if the driver requires specific formatting (e.g., `$1` for Postgres).
    - When modifying tables, ensure migrations are handled correctly and tested for idempotency (e.g., using `IF NOT EXISTS` or checking for existing columns).
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
