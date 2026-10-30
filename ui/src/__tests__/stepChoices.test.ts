import { describe, expect, it } from 'vitest'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { STEP_CHOICES, stepChoices, searchSteps } from '@/pages/workflows/WorkflowEditor/picker/stepChoices'

describe('the add-a-step choices', () => {
  it('are seven plain questions, in the order a flow reads', () => {
    expect(STEP_CHOICES.map((c) => c.title)).toEqual([
      'Read from somewhere',
      'Only keep some records',
      'Change the data',
      'Look something up',
      'Branch, wait or approve',
      'Run code or SQL',
      'Ask AI',
      'Send somewhere',
    ])
  })

  it('place every one of the node types in exactly one choice', () => {
    const all = NODE_CATEGORIES.flatMap((c) => c.items)
    const choices = stepChoices({ sources: [], sinks: [], plugins: [] })
    const placed = choices.flatMap((c) => c.items)
    expect(placed).toHaveLength(all.length)
    for (const item of all) {
      const homes = choices.filter((c) => c.items.some((i) => i.label === item.label && i.type === item.type && i.subType === item.subType))
      expect(homes.map((h) => h.title), item.label).toHaveLength(1)
    }
  })

  it('sort the well known ones where a person would look', () => {
    const by = (title: string) => stepChoices({ sources: [], sinks: [], plugins: [] }).find((c) => c.title === title)!.items.map((i) => i.label)
    expect(by('Read from somewhere')).toContain('PostgreSQL')
    expect(by('Only keep some records')).toEqual(expect.arrayContaining(['Filter', 'Validator']))
    expect(by('Change the data')).toEqual(expect.arrayContaining(['Mapping', 'Set Fields', 'Flatten']))
    expect(by('Look something up')).toEqual(expect.arrayContaining(['DB Lookup', 'API Lookup', 'Join / Enrich']))
    expect(by('Branch, wait or approve')).toEqual(expect.arrayContaining(['Condition (If)', 'Content Router', 'Approval (Human Gate)', 'Note']))
    expect(by('Run code or SQL')).toEqual(expect.arrayContaining(['Lua Script', 'WASM Transform', 'Execute SQL']))
    expect(by('Ask AI')).toEqual(expect.arrayContaining(['AI Prompt', 'Predict']))
    expect(by('Send somewhere')).toContain('Snowflake')
  })

  it('list saved connections first and tag installed plugins', () => {
    const choices = stepChoices({
      sources: [{ id: 's1', name: 'orders-db', type: 'postgresql' }],
      sinks: [{ id: 'k1', name: 'warehouse', type: 'snowflake' }],
      plugins: [{ id: 'p1', name: 'Acme scorer', type: 'wasm', description: 'Scores leads' }],
    })
    expect(choices.find((c) => c.title === 'Read from somewhere')!.items[0]).toMatchObject({ label: 'orders-db', refId: 's1', type: 'source' })
    expect(choices.find((c) => c.title === 'Send somewhere')!.items[0]).toMatchObject({ label: 'warehouse', refId: 'k1', type: 'sink' })
    const plugin = choices.find((c) => c.title === 'Change the data')!.items.find((i) => i.label === 'Acme scorer')
    expect(plugin).toMatchObject({ plugin: true, pluginID: 'p1' })
  })

  it('search across all of them by name, kind and what they do', () => {
    const choices = stepChoices({ sources: [], sinks: [], plugins: [] })
    const kafka = searchSteps(choices, 'kafka')
    expect(kafka.map((r) => `${r.item.type}:${r.item.label}`)).toEqual(expect.arrayContaining(['source:Kafka', 'sink:Kafka']))
    expect(searchSteps(choices, 'drop records').map((r) => r.item.label)).toContain('Filter')
    expect(searchSteps(choices, '')).toHaveLength(0)
  })
})
