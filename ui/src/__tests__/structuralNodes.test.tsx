import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { http, HttpResponse } from 'msw'
import { useState, type ComponentType } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import {
  NODE_TYPE_CONFIGS,
  TRANSFORM_CONFIGS,
  resolveConfigComponent,
} from '@/components/workflow/Transformation/configs/registry'
import { FlattenConfig } from '@/components/workflow/Transformation/configs/structure/FlattenConfig'
import { ExplodeConfig } from '@/components/workflow/Transformation/configs/structure/ExplodeConfig'
import { ParseFieldConfig } from '@/components/workflow/Transformation/configs/structure/ParseFieldConfig'
import { TemplateRenderConfig } from '@/components/workflow/Transformation/configs/structure/TemplateRenderConfig'
import { FieldDiffConfig } from '@/components/workflow/Transformation/configs/structure/FieldDiffConfig'
import { ReferenceLookupConfig } from '@/components/workflow/Transformation/configs/structure/ReferenceLookupConfig'
import { GeoConfig } from '@/components/workflow/Transformation/configs/structure/GeoConfig'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { canvasNodeTypes } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'
import { detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { rendersNodeEditor } from '@/pages/workflows/WorkflowEditor/components/nodeEditorSurfaces'
import { guideFor } from '@/lib/transformationGuide'

/**
 * The structural nodes: flatten / unflatten, explode, parse_field,
 * template_render, field_diff, reference_lookup and geo. Each editor saves the
 * keys the Go side reads (pkg/comm/transformer/structure, .../lookup,
 * .../geo and the explode node executor).
 */

function renderEditor(Editor: ComponentType<any>, props: Record<string, any> = {}, initial: Record<string, any> = {}) {
  let last: Record<string, any> = initial
  function Harness() {
    const [config, setConfig] = useState<Record<string, any>>(initial)
    return (
      <Editor
        nodeId="n1"
        config={config}
        fieldPaths={['lines', 'country', 'lat', 'lon']}
        updateNodeConfig={(_id: string, patch: any) => {
          setConfig((c) => {
            const next = { ...c, ...patch }
            last = next
            return next
          })
        }}
        {...props}
      />
    )
  }
  render(
    <MantineProvider>
      <Harness />
    </MantineProvider>,
  )
  return () => last
}

describe('structural nodes in the palette', () => {
  const items = NODE_CATEGORIES.flatMap((c) => c.items.map((i: any) => ({ ...i, category: c.title })))

  it.each([
    ['flatten', 'transformation', 'Structure & Parsing'],
    ['unflatten', 'transformation', 'Structure & Parsing'],
    ['explode', 'explode', 'Structure & Parsing'],
    ['parse_field', 'transformation', 'Structure & Parsing'],
    ['template_render', 'transformation', 'Structure & Parsing'],
    ['field_diff', 'transformation', 'Structure & Parsing'],
    ['reference_lookup', 'transformation', 'Advanced Transformations'],
    ['geo', 'transformation', 'Advanced Transformations'],
  ])('%s is offered as a %s node under %s, with an editor and a guide', (subType, type, category) => {
    const item = items.find((i) => i.subType === subType && i.type === type)
    expect(item?.category).toBe(category)
    expect(resolveConfigComponent(type, subType)).toBeTruthy()
    expect(guideFor(subType, type).what).not.toBe('')
  })

  it('draws and configures the explode node', () => {
    expect(NODE_TYPE_CONFIGS.explode).toBe(ExplodeConfig)
    expect(canvasNodeTypes.explode).toBeTruthy()
    expect(detailNodeTypes.explode).toBeTruthy()
    expect(rendersNodeEditor('explode')).toBe(true)
  })

  it('serves flatten and unflatten with one editor', () => {
    expect(TRANSFORM_CONFIGS.flatten).toBe(FlattenConfig)
    expect(TRANSFORM_CONFIGS.unflatten).toBe(FlattenConfig)
  })
})

describe('structural node editors', () => {
  it('flatten saves the separator and how arrays are treated', async () => {
    const config = renderEditor(FlattenConfig, { transType: 'flatten' })
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox', { name: /separator/i }), '.')
    await user.click(screen.getByLabelText(/keep as arrays/i))
    await user.type(screen.getByRole('textbox', { name: /max depth/i }), '2')
    expect(config()).toMatchObject({ separator: '.', arrays: 'keep', maxDepth: '2' })
  })

  it('explode saves the array, the merge mode and the cap', async () => {
    const config = renderEditor(ExplodeConfig, { nodeType: 'explode' })
    const user = userEvent.setup()
    expect(screen.getByText(/array path is required/i)).toBeInTheDocument()
    await user.type(screen.getByRole('combobox', { name: /array path/i }), 'lines')
    await user.click(screen.getByLabelText(/merge into the record/i))
    expect(screen.queryByRole('textbox', { name: /target field/i })).toBeNull()
    await user.type(screen.getByRole('textbox', { name: /max items/i }), '50')
    expect(config()).toMatchObject({ arrayPath: 'lines', mode: 'merge', maxItems: '50' })
  })

  it('parse_field offers the CSV settings once CSV is chosen', async () => {
    const config = renderEditor(ParseFieldConfig)
    const user = userEvent.setup()
    await user.type(screen.getByRole('combobox', { name: /^field/i }), 'raw')
    expect(screen.queryByRole('textbox', { name: /headers/i })).toBeNull()
    await user.click(screen.getByLabelText('CSV'))
    await user.type(screen.getByRole('textbox', { name: /headers/i }), 'id,name')
    expect(config()).toMatchObject({ field: 'raw', format: 'csv', headers: 'id,name' })
  })

  it('template_render saves the template, target and strict mode', async () => {
    const config = renderEditor(TemplateRenderConfig)
    const user = userEvent.setup()
    await user.click(screen.getByRole('textbox', { name: /template/i }))
    await user.paste('Hello {{.name}}')
    await user.type(screen.getByRole('textbox', { name: /target field/i }), 'greeting')
    await user.click(screen.getByRole('switch', { name: /strict/i }))
    expect(config()).toMatchObject({ template: 'Hello {{.name}}', targetField: 'greeting', strict: true })
  })

  it('field_diff saves the ignored columns and the drop switch', async () => {
    const config = renderEditor(FieldDiffConfig)
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox', { name: /ignore columns/i }), 'updated_at')
    await user.click(screen.getByRole('switch', { name: /drop records with no changes/i }))
    expect(config()).toMatchObject({ ignoreColumns: 'updated_at', dropUnchanged: true })
  })

  it('reference_lookup uploads the file and keeps the path the server stored it at', async () => {
    signInAs('Editor')
    let uploaded = false
    server.use(
      http.post('/api/files/upload', async ({ request }) => {
        // jsdom's File does not survive the trip into the handler's FormData
        // intact (name and content are lost), so only the part is checked.
        const form = await request.formData()
        uploaded = form.has('file')
        return HttpResponse.json({ path: '/var/hermod/uploads/countries-1.csv' })
      }),
    )
    const config = renderEditor(ReferenceLookupConfig)
    const user = userEvent.setup()
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    await user.upload(input, new File(['code,name\nFR,France\n'], 'countries.csv', { type: 'text/csv' }))
    await waitFor(() => expect(config().filePath).toBe('/var/hermod/uploads/countries-1.csv'))
    expect(uploaded).toBe(true)

    await user.type(screen.getByRole('textbox', { name: /key column/i }), 'code')
    await user.type(screen.getByRole('combobox', { name: /key field/i }), 'country')
    expect(config()).toMatchObject({ keyColumn: 'code', keyField: 'country' })
  })

  it('geo switches between distance and polygon, and flags a polygon that is not JSON', async () => {
    const config = renderEditor(GeoConfig)
    const user = userEvent.setup()
    await user.type(screen.getByRole('combobox', { name: /first point latitude/i }), 'a.lat')
    await user.click(screen.getByLabelText('Miles'))
    expect(config()).toMatchObject({ lat1Field: 'a.lat', unit: 'mi' })

    await user.click(screen.getByLabelText(/inside polygon/i))
    await user.click(screen.getByRole('textbox', { name: /polygon/i }))
    await user.paste('{"type":')
    expect(await screen.findByText(/not valid json/i)).toBeInTheDocument()
    expect(config()).toMatchObject({ operation: 'within', polygon: '{"type":' })
  })
})
