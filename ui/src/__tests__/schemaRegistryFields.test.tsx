import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueueSinkConfig } from '../components/workflow/Sink/QueueSinkConfig'

/**
 * The Confluent Schema Registry keys are read by `buildFormatter` in
 * internal/factory. A key the backend reads and the form never writes is a
 * feature nobody can reach from the UI, which is the shape this repository has
 * shipped more than once.
 *
 * These assert the names as strings on purpose. Renaming a key on either side
 * is exactly the drift being guarded against, so the test must not import the
 * name from the code it is checking.
 */

const renderKafka = (config: Record<string, string> = {}) => {
  const updateConfig = vi.fn()
  render(
    <MantineProvider>
      <QueueSinkConfig type="kafka" config={config} updateConfig={updateConfig} />
    </MantineProvider>,
  )
  return updateConfig
}

describe('Kafka sink exposes the schema registry fields', () => {
  it('offers a Message Format selector', () => {
    renderKafka()
    // Mantine's Select renders a label and a combobox input that both match
    // the accessible name, so a plain label query is ambiguous here.
    expect(screen.getByRole('combobox', { name: /message format/i })).toBeInTheDocument()
  })

  it('hides the registry fields until the format selects them', () => {
    renderKafka({ format: 'json' })
    expect(screen.queryByLabelText(/schema registry url/i)).not.toBeInTheDocument()
  })

  it('shows url, subject and schema once format=schema_registry', () => {
    renderKafka({ format: 'schema_registry' })
    expect(screen.getByLabelText(/schema registry url/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/subject/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/schema/i, { selector: 'textarea' })).toBeInTheDocument()
  })

  it.each([
    ['schema registry url', 'schema_registry_url', 'http://sr:8081'],
    ['subject', 'schema_registry_subject', 'users-value'],
  ])('writes %s to the %s config key', async (label, key, value) => {
    const updateConfig = renderKafka({ format: 'schema_registry' })
    await userEvent.type(screen.getByLabelText(new RegExp(label, 'i')), value)
    expect(updateConfig).toHaveBeenCalledWith(key, expect.any(String))
  })

  it('defaults the schema type to Avro and says Protobuf is unavailable', () => {
    renderKafka({ format: 'schema_registry' })

    // Mantine v9 renders its options in a Combobox dropdown that does not open
    // under a synthetic click, so enumerating them here is not reachable. The
    // binding assertion for Protobuf lives where the refusal actually is:
    // TestSchemaRegistryFormatRefusesIncompleteConfig/protobuf in
    // internal/factory. What this checks is the part only the UI can get
    // wrong — the default, and whether the operator is told.
    expect(screen.getByRole('combobox', { name: /schema type/i })).toHaveValue('Avro')
    expect(screen.getByText(/protobuf is not offered/i)).toBeInTheDocument()
  })

  it('marks the registry credential field as a password', () => {
    renderKafka({ format: 'schema_registry' })
    const pw = screen.getByLabelText(/registry password|api secret/i)
    expect(pw).toHaveAttribute('type', 'password')
  })
})
