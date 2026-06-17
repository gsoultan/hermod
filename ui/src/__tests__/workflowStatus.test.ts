import { describe, it, expect } from 'vitest'
import { normalizeWorkflowStatus } from '@/utils/workflowStatus'

describe('normalizeWorkflowStatus', () => {
  const testCases: { name: string; input: string | null | undefined; label: string; color: string }[] = [
    { name: 'running', input: 'running', label: 'Running', color: 'green' },
    { name: 'active alias', input: 'active', label: 'Running', color: 'green' },
    { name: 'reconnecting source', input: 'reconnecting:source', label: 'Reconnecting', color: 'orange' },
    { name: 'reconnecting sink', input: 'reconnecting:sink:s1', label: 'Reconnecting', color: 'orange' },
    { name: 'restarting', input: 'Restarting', label: 'Restarting', color: 'orange' },
    { name: 'connecting', input: 'connecting', label: 'Connecting', color: 'orange' },
    { name: 'starting alias', input: 'starting', label: 'Connecting', color: 'orange' },
    { name: 'stopping', input: 'stopping', label: 'Stopping', color: 'orange' },
    { name: 'stopped', input: 'stopped', label: 'Stopped', color: 'gray' },
    { name: 'inactive alias', input: 'inactive', label: 'Stopped', color: 'gray' },
    { name: 'paused', input: 'paused', label: 'Paused', color: 'yellow' },
    { name: 'completed', input: 'completed', label: 'Completed', color: 'blue' },
    { name: 'failed', input: 'failed', label: 'Error', color: 'red' },
    { name: 'error prefix', input: 'error: boom', label: 'Error', color: 'red' },
    { name: 'circuit breaker open', input: 'circuit_breaker_open', label: 'Error', color: 'red' },
    { name: 'undefined', input: undefined, label: 'Unknown', color: 'gray' },
    { name: 'null', input: null, label: 'Unknown', color: 'gray' },
    { name: 'empty', input: '   ', label: 'Unknown', color: 'gray' },
    { name: 'unknown title-cased', input: 'draining_buffer', label: 'Draining Buffer', color: 'blue' },
  ]

  testCases.forEach(({ name, input, label, color }) => {
    it(name, () => {
      const result = normalizeWorkflowStatus(input)
      expect(result.label).toBe(label)
      expect(result.color).toBe(color)
    })
  })
})
