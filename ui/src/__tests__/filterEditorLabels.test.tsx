import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { FilterEditor } from '@/components/workflow/Transformation/FilterEditor'

// Each condition row's Field, Operator and Value carried a visible caption but
// no accessible name: a screen reader announced the field by its placeholder
// ("e.g. status, lower(source.name)"), the operator as an unnamed combobox,
// and gave no way to tell the rows apart.
describe('FilterEditor condition rows', () => {
  it('name every input by its row', () => {
    render(
      <MantineProvider>
        <FilterEditor
          conditions={[
            { field: 'status', operator: '=', value: 'paid' },
            { field: 'array_len(source.users)', operator: '>', value: '0' },
          ]}
          availableFields={[{ path: 'status' }, { path: 'users' }]}
          onChange={() => {}}
        />
      </MantineProvider>,
    )
    expect(screen.getByRole('combobox', { name: 'Condition 1 field' })).toHaveValue('status')
    expect(screen.getByRole('combobox', { name: 'Condition 1 operator' })).toHaveValue('=')
    expect(screen.getByRole('textbox', { name: 'Condition 1 value' })).toHaveValue('paid')
    expect(screen.getByRole('combobox', { name: 'Condition 2 field' })).toHaveValue('array_len(source.users)')
    expect(screen.getByRole('combobox', { name: 'Condition 2 operator' })).toHaveValue('>')
    expect(screen.getByRole('textbox', { name: 'Condition 2 value' })).toHaveValue('0')
  })
})
