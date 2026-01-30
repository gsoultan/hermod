import { Alert, Code, Grid, List, Stack, Text } from '@mantine/core'

export default function HelpContent() {
  return (
    <Stack gap="sm">
      <Alert color="blue" variant="light">
        <Stack gap={4}>
          <Text size="sm" fw={700}>How to use advanced expressions:</Text>
          <Text size="sm">1. Use format: <Code>operation(source.field)</Code> or <Code>operation("literal")</Code></Text>
          <Text size="sm">2. Support nesting: <Code>upper(trim(source.name))</Code></Text>
          <Text size="sm">3. Use <Code>source.path</Code> for input fields and quotes for strings.</Text>
        </Stack>
      </Alert>

      <Text size="sm" fw={700}>Supported operations</Text>
      <Grid gutter="xs">
        <Grid.Col span={{ base: 12, sm: 4 }}>
          <List size="sm">
            <List.Item><Code>lower</Code>, <Code>upper</Code>, <Code>trim</Code></List.Item>
            <List.Item><Code>concat(a, b, ...)</Code></List.Item>
            <List.Item><Code>substring(s, start, [end])</Code></List.Item>
            <List.Item><Code>coalesce(a, b, ...)</Code></List.Item>
          </List>
        </Grid.Col>
        <Grid.Col span={{ base: 12, sm: 4 }}>
          <List size="sm">
            <List.Item><Code>add</Code>, <Code>sub</Code>, <Code>mul</Code>, <Code>div</Code></List.Item>
            <List.Item><Code>abs(n)</Code>, <Code>round(n, [p])</Code></List.Item>
            <List.Item><Code>now()</Code>, <Code>hash(s, [a])</Code></List.Item>
            <List.Item><Code>if(cond, t, f)</Code></List.Item>
          </List>
        </Grid.Col>
        <Grid.Col span={{ base: 12, sm: 4 }}>
          <List size="sm">
            <List.Item><Code>and</Code>, <Code>or</Code>, <Code>not</Code></List.Item>
            <List.Item><Code>eq</Code>, <Code>gt</Code>, <Code>lt</Code>, <Code>contains</Code></List.Item>
            <List.Item><Code>toInt</Code>, <Code>toFloat</Code></List.Item>
            <List.Item><Code>toString</Code>, <Code>toBool</Code></List.Item>
          </List>
        </Grid.Col>
      </Grid>
    </Stack>
  )
}
