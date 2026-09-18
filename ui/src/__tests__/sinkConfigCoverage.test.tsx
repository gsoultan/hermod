import { describe, it, expect } from 'vitest'
import { SINK_TYPES, configComponents } from '@/components/forms/SinkForm'
import { missingConnectionFields, typesWithRequirements } from '@/lib/connectorRequirements'

/**
 * SinkWizard resolves a type's form as
 * `configComponents[sink.type] || configComponents['database']`, so a type
 * missing from the map does not fail loudly — it renders the *database* form.
 *
 * That is how "API / Webhook" came to show host/port/table fields: `http` was
 * never in the map. Worse than cosmetic, because the database form writes no
 * `url` key, `SINK_REQUIREMENTS.http` demands one, and SinkWizard disables
 * Next and Save while anything is missing. The sink became unconfigurable from
 * every entry point at once, silently.
 *
 * These two gates are what make that impossible to reintroduce.
 */
describe('every offered sink type has its own configuration form', () => {
  const database = configComponents['database']

  it.each(SINK_TYPES.map((t) => [t.value, t.label] as const))(
    '%s (%s) does not fall through to the database form',
    (value) => {
      expect(
        Object.prototype.hasOwnProperty.call(configComponents, value),
        `sink type "${value}" has no entry in configComponents, so SinkWizard renders the database form instead`,
      ).toBe(true)
    },
  )

  it('only the database types are served by the database form', () => {
    const servedByDatabase = SINK_TYPES.map((t) => t.value).filter(
      (value) => configComponents[value] === database,
    )
    // MongoDB is deliberate: DatabaseSinkConfig writes uri/database/table and
    // the column mappings, which is exactly what the factory reads for it.
    expect(servedByDatabase.sort()).toEqual([
      'clickhouse',
      'mariadb',
      'mongodb',
      'mssql',
      'mysql',
      'oracle',
      'sqlite',
      'yugabyte',
    ])
  })
})

/**
 * The second half of the same defect: a form that cannot satisfy its own gate.
 * A required key the rendered component never writes is a Next button that
 * never enables, which is indistinguishable from a broken save.
 *
 * `writableKeys` is the set of config keys each form calls `updateConfig` with,
 * read from the component source. Source-scraping is blunt, but it is what
 * catches the mismatch without mounting 48 lazy components.
 */
describe('a sink form can satisfy the requirements gate it is held to', () => {
  const requiredKeys: Record<string, string[]> = {
    http: ['url'],
    websocket: ['url'],
    elasticsearch: ['addresses'],
    snowflake: ['connection_string'],
    redis: ['addr'],
    kafka: ['brokers', 'topic'],
    s3: ['bucket', 'region'],
    mqtt: ['broker_url', 'topic'],
    file: ['filename'],
    eventstore: ['driver', 'dsn'],
    panmail: ['base_url', 'api_key', 'provider_id', 'from', 'to'],
    metis: ['base_url', 'project_id', 'token', 'definition_key'],
  }

  it.each(Object.entries(requiredKeys))(
    'a %s sink with its required keys filled leaves the connection step',
    (type, keys) => {
      const config = Object.fromEntries(keys.map((k) => [k, 'x']))
      expect(missingConnectionFields('sink', type, config)).toEqual([])
    },
  )

  it('names what is still missing when a required key is blank', () => {
    expect(missingConnectionFields('sink', 'http', {})).toEqual(['Destination URL'])
    expect(missingConnectionFields('sink', 'websocket', {})).toEqual(['WebSocket URL'])
  })
})

/**
 * The gate the comment above promised but nothing implemented: compare the keys
 * a form actually writes against the keys its type is gated on.
 *
 * Both halves of the s3-parquet defect hid in that gap. `s3-parquet` was mapped
 * to the shared S3SinkConfig, which writes `s3_region`/`s3_bucket`, while the
 * factory and the requirements list read `region`/`bucket` — so the sink was
 * built with every field empty and had nowhere to put the parquet schema it
 * cannot write a single row without. And the requirements entry was keyed
 * `s3parquet` while every sink of that type is `s3-parquet`, so the gate that
 * would have caught it never ran.
 */
describe('a sink form writes the keys its type is gated on', () => {
  // Read through Vite rather than node:fs: this project's tsconfig has no node
  // types, and ?raw is the toolchain's own way to get a module's text.
  const sources = import.meta.glob('../components/**/*.tsx', {
    query: '?raw',
    eager: true,
    import: 'default',
  }) as Record<string, string>
  const byBasename = new Map<string, string>()
  for (const [path, src] of Object.entries(sources)) {
    byBasename.set(path.slice(path.lastIndexOf('/') + 1).replace(/\.tsx$/, ''), src)
  }

  const sinkFormSrc = byBasename.get('SinkForm')!

  // component name -> module specifier, from the lazy() imports.
  const importPaths = new Map<string, string>()
  for (const m of sinkFormSrc.matchAll(/const (\w+) = lazy\(\(\) => import\('([^']+)'\)/g)) {
    importPaths.set(m[1], m[2])
  }
  // sink type -> component name, from the configComponents map.
  const componentFor = new Map<string, string>()
  for (const m of sinkFormSrc.matchAll(/^\s*'?([\w-]+)'?\s*:\s*(\w+),\s*$/gm)) {
    if (importPaths.has(m[2])) componentFor.set(m[1], m[2])
  }

  const sourceOf = (type: string): string | undefined => {
    const component = componentFor.get(type)
    const spec = component && importPaths.get(component)
    if (!spec) return undefined
    return byBasename.get(spec.slice(spec.lastIndexOf('/') + 1))
  }

  it.each(SINK_TYPES.map((t) => [t.value] as const))('%s', (type) => {
    const src = sourceOf(type)
    expect(src, `could not resolve the form module for sink type "${type}"`).toBeDefined()

    // A form that computes the key at runtime cannot be read this way; it is
    // not evidence of a mismatch, so it is not evidence of a match either.
    if (/updateConfig\(\s*[^'"`)]/.test(src!)) return

    const written = Object.fromEntries(
      [...src!.matchAll(/updateConfig\(\s*'([^']+)'/g)].map((m) => [m[1], 'x']),
    )
    expect(
      missingConnectionFields('sink', type, written),
      `the form for "${type}" never writes these keys, so its Next button cannot enable`,
    ).toEqual([])
  })
})

/**
 * A requirements entry keyed by a type string no sink ever has is a gate that
 * silently never runs.
 */
describe('every sink requirements entry is reachable', () => {
  it('is keyed by a type the sink form offers', () => {
    // Either entry point counts: a type offered in the dropdown, or one that
    // has a form mapped to it and is reachable by saved config.
    const known = new Set([...SINK_TYPES.map((t) => t.value), ...Object.keys(configComponents)])
    const unreachable = typesWithRequirements('sink').filter((t) => !known.has(t))
    expect(unreachable).toEqual([])
  })
})
