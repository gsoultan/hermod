import { useState } from 'react'
import { Stack, Group, Text, Button, ActionIcon, Paper, TextInput, JsonInput, Badge, Alert, Loader, Collapse, Code } from '@mantine/core'
import { IconPlus, IconTrash, IconPlayerPlay, IconCheck, IconX, IconAlertCircle, IconChevronDown, IconChevronUp } from '@tabler/icons-react'
import { apiFetch } from '../api'

interface UnitTest {
  name: string
  input: any
  expected_output: any
  description?: string
}

interface UnitTestFormProps {
  workflowId: string
  nodeId: string
  tests: UnitTest[]
  onChange: (tests: UnitTest[]) => void
}

export function UnitTestForm({ workflowId, nodeId, tests = [], onChange }: UnitTestFormProps) {
  const [running, setRunning] = useState(false)
  const [results, setResults] = useState<any[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<number | null>(null)

  const addTest = () => {
    const newTest: UnitTest = {
      name: `Test Case ${tests.length + 1}`,
      input: {},
      expected_output: {}
    }
    onChange([...tests, newTest])
  }

  const removeTest = (index: number) => {
    const newTests = [...tests]
    newTests.splice(index, 1)
    onChange(newTests)
  }

  const updateTest = (index: number, field: keyof UnitTest, value: any) => {
    const newTests = [...tests]
    newTests[index] = { ...newTests[index], [field]: value }
    onChange(newTests)
  }

  const runTests = async () => {
    if (!workflowId || workflowId === 'new') {
      setError('Save the workflow first before running tests.')
      return
    }
    setRunning(true)
    setError(null)
    try {
      const res = await apiFetch(`/api/workflows/${workflowId}/nodes/${nodeId}/test`, {
        method: 'POST'
      })
      if (!res.ok) {
        const data = await res.json()
        throw new Error(data.error || 'Failed to run tests')
      }
      const data = await res.json()
      setResults(data)
    } catch (e: any) {
      setError(e.message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Text fw={700} size="sm">Unit Tests</Text>
        <Group gap="xs">
          <Button 
            size="compact-xs" 
            variant="light" 
            color="green" 
            leftSection={running ? <Loader size={10} /> : <IconPlayerPlay size={12} />} 
            onClick={runTests}
            disabled={running || tests.length === 0}
          >
            Run All
          </Button>
          <Button size="compact-xs" variant="outline" leftSection={<IconPlus size={12} />} onClick={addTest}>
            Add Test
          </Button>
        </Group>
      </Group>

      {error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />}>
          {error}
        </Alert>
      )}

      {tests.length === 0 && (
        <Paper withBorder p="md" radius="md" style={{ borderStyle: 'dashed' }}>
          <Text size="xs" c="dimmed" ta="center">No test cases defined. Add one to ensure your logic works as expected.</Text>
        </Paper>
      )}

      <Stack gap="xs">
        {tests.map((test, index) => {
          const result = results?.[index]
          const isExpanded = expanded === index

          return (
            <Paper key={index} withBorder radius="md" p="xs">
              <Stack gap="xs">
                <Group justify="space-between">
                  <Group gap="xs">
                    <ActionIcon 
                      variant="subtle" 
                      size="sm" 
                      onClick={() => setExpanded(isExpanded ? null : index)}
                    >
                      {isExpanded ? <IconChevronUp size={14} /> : <IconChevronDown size={14} />}
                    </ActionIcon>
                    <TextInput 
                      variant="unstyled" 
                      value={test.name} 
                      onChange={(e) => updateTest(index, 'name', e.currentTarget.value)}
                      styles={{ input: { fontWeight: 600, fontSize: '13px' } }}
                    />
                  </Group>
                  <Group gap="xs">
                    {result && (
                      <Badge 
                        color={result.passed ? 'green' : 'red'} 
                        variant="light" 
                        size="xs"
                        leftSection={result.passed ? <IconCheck size={10} /> : <IconX size={10} />}
                      >
                        {result.passed ? 'Passed' : 'Failed'}
                      </Badge>
                    )}
                    <ActionIcon color="red" variant="subtle" size="sm" onClick={() => removeTest(index)}>
                      <IconTrash size={14} />
                    </ActionIcon>
                  </Group>
                </Group>

                <Collapse in={isExpanded}>
                  <Stack gap="xs" mt="xs">
                    <JsonInput 
                      label="Input Payload" 
                      placeholder="{}" 
                      value={typeof test.input === 'string' ? test.input : JSON.stringify(test.input, null, 2)}
                      onChange={(val) => {
                        try { updateTest(index, 'input', JSON.parse(val)) } catch(e) { updateTest(index, 'input', val) }
                      }}
                      minRows={4}
                      size="xs"
                      styles={{ input: { fontFamily: 'monospace' } }}
                    />
                    <JsonInput 
                      label="Expected Output (Partial Match)" 
                      placeholder="{}" 
                      value={typeof test.expected_output === 'string' ? test.expected_output : JSON.stringify(test.expected_output, null, 2)}
                      onChange={(val) => {
                        try { updateTest(index, 'expected_output', JSON.parse(val)) } catch(e) { updateTest(index, 'expected_output', val) }
                      }}
                      minRows={4}
                      size="xs"
                      styles={{ input: { fontFamily: 'monospace' } }}
                    />
                    {result && !result.passed && (
                      <Alert color="red" title="Mismatch detected" p="xs">
                        <Text size="xs" fw={700}>Actual Output:</Text>
                        <Code block style={{ fontSize: '10px' }}>{JSON.stringify(result.actual, null, 2)}</Code>
                        {result.error && <Text size="xs" c="red" mt={4}>Error: {result.error}</Text>}
                      </Alert>
                    )}
                  </Stack>
                </Collapse>
              </Stack>
            </Paper>
          )
        })}
      </Stack>
    </Stack>
  )
}
