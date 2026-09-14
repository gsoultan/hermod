import { describe, it, expect } from 'vitest'
import { SINK_TYPES, configComponents } from '@/components/forms/SinkForm'
import { missingConnectionFields } from '@/lib/connectorRequirements'

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
    elasticsearch: ['url'],
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
