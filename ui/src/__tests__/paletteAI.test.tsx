import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidebarDrawer } from '@/pages/workflows/WorkflowEditor/components/SidebarDrawer'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { NODE_CATEGORIES, paletteItemData } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import {
  NODE_TYPE_CONFIGS,
  TRANSFORM_CONFIGS,
  resolveConfigComponent,
} from '@/components/workflow/Transformation/configs/registry'
import { AIPromptConfig } from '@/components/workflow/Transformation/configs/ai/AIPromptConfig'
import { AIExtractConfig } from '@/components/workflow/Transformation/configs/ai/AIExtractConfig'
import { AIEmbedConfig } from '@/components/workflow/Transformation/configs/ai/AIEmbedConfig'
import { AIClassifyConfig } from '@/components/workflow/Transformation/configs/ai/AIClassifyConfig'
import { rendersNodeEditor } from '@/pages/workflows/WorkflowEditor/components/nodeEditorSurfaces'

const aiCategory = () => NODE_CATEGORIES.find((c) => c.title === 'AI')

describe('the AI palette category', () => {
  it('offers the AI nodes', () => {
    const items = aiCategory()?.items ?? []
    expect(items.map((i: any) => [i.label, i.type, i.subType])).toEqual([
      ['AI Prompt', 'transformation', 'ai_prompt'],
      ['AI Extract', 'transformation', 'ai_extract'],
      ['AI Classify', 'ai_classify', 'ai_classify'],
      ['Summarize', 'transformation', 'ai_prompt'],
      ['Translate', 'transformation', 'ai_prompt'],
      ['AI Embed', 'transformation', 'ai_embed'],
      ['AI Retrieve', 'transformation', 'ai_retrieve'],
    ])
    expect(aiCategory()?.group).toBe('transformations')
  })

  it('presets Summarize and Translate as prompts', () => {
    const summarize = aiCategory()!.items.find((i: any) => i.label === 'Summarize')!
    expect(paletteItemData(summarize)).toEqual({
      prompt: 'Summarise the input in three sentences.',
      includeData: true,
    })
    const translate = aiCategory()!.items.find((i: any) => i.label === 'Translate')!
    expect(paletteItemData(translate)?.prompt).toMatch(/translate/i)
  })

  it('no longer offers the old AI Enrichment and AI Mapper items', () => {
    const subTypes = NODE_CATEGORIES.flatMap((c) => c.items).map((i: any) => i.subType)
    expect(subTypes).not.toContain('ai_enrichment')
    expect(subTypes).not.toContain('ai_mapper')
  })

  // Workflows saved with them must still open with an editor.
  it('keeps the old AI editors registered', () => {
    expect(TRANSFORM_CONFIGS.ai_enrichment).toBeTruthy()
    expect(TRANSFORM_CONFIGS.ai_mapper).toBeTruthy()
  })

  it('registers an editor for each AI node', () => {
    expect(resolveConfigComponent('transformation', 'ai_prompt')).toBe(AIPromptConfig)
    expect(resolveConfigComponent('transformation', 'ai_extract')).toBe(AIExtractConfig)
    expect(resolveConfigComponent('transformation', 'ai_embed')).toBe(AIEmbedConfig)
    expect(NODE_TYPE_CONFIGS.ai_classify).toBe(AIClassifyConfig)
    expect(rendersNodeEditor('ai_classify')).toBe(true)
  })

  it('passes a plugin id and a preset together', () => {
    expect(paletteItemData({ pluginID: 'p1', defaults: { a: 1 } })).toEqual({ pluginID: 'p1', a: 1 })
    expect(paletteItemData({})).toBeUndefined()
  })
})

describe('adding a preset from the palette', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    useWorkflowStore.setState({
      drawerOpened: true,
      drawerTab: 'transformations',
      nodes: [{ id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders' } }] as any,
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  it('hands the preset to onAddItem and onDragStart as the node data', async () => {
    const user = userEvent.setup()
    const onAddItem = vi.fn()
    const onDragStart = vi.fn()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={onDragStart} onAddItem={onAddItem} sources={[]} sinks={[]} />
        </QueryClientProvider>
      </MantineProvider>,
    )
    const tab = within(screen.getByRole('tabpanel', { name: 'Transformations' }))
    await user.click(tab.getByText('Summarize', { exact: true }))

    const call = onAddItem.mock.calls.at(-1)!
    expect(call.slice(0, 4)).toEqual(['transformation', 'new', 'Summarize', 'ai_prompt'])
    expect(call[6]).toEqual({ prompt: 'Summarise the input in three sentences.', includeData: true })
  })
})
