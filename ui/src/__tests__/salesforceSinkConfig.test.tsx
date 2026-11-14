import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { SalesforceSinkConfig } from '@/components/workflow/Sink/SalesforceSinkConfig'

// The sink wizard renders every sink's fields with config and updateConfig
// (SinkWizard.tsx). This one alone expected a Mantine form object it is never
// given, so choosing Salesforce on Add sink threw on `form.getInputProps` and
// the page fell to the error screen.
describe('SalesforceSinkConfig', () => {
  const renderWith = (config: Record<string, any>, updateConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <SalesforceSinkConfig config={config} updateConfig={updateConfig} />
      </MantineProvider>,
    )
    return updateConfig
  }

  it('renders from the props the wizard passes, and writes the factory keys', () => {
    const update = renderWith({})
    for (const label of ['Client ID', 'Client Secret', 'Username', 'Password', 'Security Token', 'SObject']) {
      expect(screen.getByLabelText(label, { exact: false })).toBeTruthy()
    }
    fireEvent.change(screen.getByRole('textbox', { name: 'Client ID' }), { target: { value: 'abc' } })
    expect(update).toHaveBeenCalledWith('client_id', 'abc')
    fireEvent.change(screen.getByRole('textbox', { name: 'SObject' }), { target: { value: 'Account' } })
    expect(update).toHaveBeenCalledWith('object', 'Account')
  })

  it('asks for the external ID only for an upsert', () => {
    renderWith({ operation: 'insert' })
    expect(screen.queryByRole('textbox', { name: 'External ID Field' })).toBeNull()
  })

  it('shows the external ID for an upsert', () => {
    renderWith({ operation: 'upsert', external_id: 'Ext__c' })
    expect(screen.getByRole('textbox', { name: 'External ID Field' })).toHaveValue('Ext__c')
  })
})
