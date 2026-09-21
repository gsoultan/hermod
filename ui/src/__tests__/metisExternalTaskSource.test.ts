import { describe, it, expect } from 'vitest'
import { missingConnectionFields } from '@/lib/connectorRequirements'
import { isNonDestructiveSample, validateSourceForSampling } from '@/components/workflow/Source/sourceSampling'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { SOURCE_TYPES } from '@/components/forms/SourceForm'
import type { Source } from '@/types'

/**
 * The external-task source is reachable only if five hand-written lists agree
 * on one string. The palette offers the type, the form's dropdown offers it,
 * the wizard gate knows what it requires, the config panel renders its fields,
 * and the factory reads the keys that panel writes. Each list is maintained
 * separately, and this repo has shipped a connector unreachable from the UI
 * more than once because one of them was missed.
 */
describe('the metis external-task source, across the lists that must agree', () => {
  const connected = { base_url: 'https://bpm.example.com', topic: 'reverse-charge', token: 'tok' }

  it('is offered in the node palette', () => {
    const subTypes = NODE_CATEGORIES.flatMap((c: any) =>
      c.items.filter((i: any) => i.type === 'source').map((i: any) => i.subType),
    )
    expect(subTypes).toContain('metis_task')
  })

  it('is offered in the source form dropdown', () => {
    expect(SOURCE_TYPES.map((t) => t.value)).toContain('metis_task')
  })

  it('asks for the engine URL and the topic', () => {
    expect(missingConnectionFields('source', 'metis_task', {})).toEqual(
      expect.arrayContaining(['Engine URL', 'Topic']),
    )
  })

  /**
   * fetch-and-lock takes a topic and no project: a topic is subscribed to
   * across the organization. Demanding a project here would disable Next on a
   * configuration that works.
   */
  it('does not demand a project — unlike the polling source, it has none', () => {
    expect(missingConnectionFields('source', 'metis_task', {})).not.toContain('Project ID')
  })

  it('is satisfied by a token', () => {
    expect(missingConnectionFields('source', 'metis_task', connected)).toEqual([])
  })

  it('is satisfied by a username instead: either authenticates', () => {
    expect(
      missingConnectionFields('source', 'metis_task', {
        base_url: 'https://bpm.example.com',
        topic: 'reverse-charge',
        username: 'svc',
      }),
    ).toEqual([])
  })

  it('names the topic when sampling is attempted without one', () => {
    const source = { id: 's1', name: 'tasks', type: 'metis_task', config: { base_url: 'https://x.example.com' } } as unknown as Source
    const result = validateSourceForSampling(source)
    expect(result.issues.join(' ')).toMatch(/topic/i)
  })

  /**
   * Sampling this source fetches and *locks* real work: the task leaves the
   * queue for the lock duration and the process stalls there, because a preview
   * never completes it. Advertising a non-destructive preview would be a lie.
   */
  it('is not advertised as a non-destructive preview', () => {
    expect(isNonDestructiveSample('metis_task')).toBe(false)
  })
})
