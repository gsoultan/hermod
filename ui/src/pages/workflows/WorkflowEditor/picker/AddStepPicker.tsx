import { useMemo, useState } from 'react';
import { ActionIcon, Badge, Box, Button, Group, Modal, SimpleGrid, Stack, Text, TextInput, ThemeIcon, UnstyledButton } from '@mantine/core';
import { IconChevronLeft, IconSearch, IconX } from '@tabler/icons-react';
import { searchSteps, stepChoices, type StepChoice, type StepItem } from './stepChoices';

interface AddStepPickerProps {
  opened: boolean;
  onClose: () => void;
  onPick: (item: StepItem) => void;
  /** Opens the full palette, with its headings, for the people who know the catalogue. */
  onOpenPalette: () => void;
  /** Transformations and sinks need something to read from first, as in the palette. */
  hasSource: boolean;
  sources: any[];
  sinks: any[];
  plugins: any[];
}

/**
 * "What should happen next?": the one question, eight plain answers, and a
 * search box over all the types behind them. Opens from Add step and from the
 * + on a node, and adds the step where the + was.
 */
export function AddStepPicker({ opened, onClose, onPick, onOpenPalette, hasSource, sources, sinks, plugins }: AddStepPickerProps) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState<StepChoice | null>(null);
  const choices = useMemo(() => stepChoices({ sources, sinks, plugins }), [sources, sinks, plugins]);
  const matches = useMemo(() => searchSteps(choices, query), [choices, query]);

  // Reset on the way out, so the next opening starts at the question again.
  const close = () => { setQuery(''); setOpen(null); onClose(); };
  const pick = (item: StepItem) => { onPick(item); close(); };
  const locked = (choice: StepChoice) => !hasSource && choice.key !== 'read';

  const row = (item: StepItem, key: string) => (
    <li key={key}>
    <UnstyledButton
      onClick={() => pick(item)}
      px="sm"
      py={8}
      style={{ display: 'block', width: '100%', borderRadius: 8 }}
      className="hermod-pick"
    >
      <Group wrap="nowrap" gap="sm">
        <ThemeIcon variant="light" color={item.color} size="lg" radius="md"><item.icon size="1.1rem" /></ThemeIcon>
        <Box style={{ flex: 1, minWidth: 0 }}>
          <Group gap={6} wrap="nowrap">
            <Text size="sm" fw={600} truncate="end">{item.label}</Text>
            {item.existing && <Badge size="xs" variant="light" color="gray">Saved</Badge>}
            {item.plugin && <Badge size="xs" variant="light" color="indigo">Plugin</Badge>}
          </Group>
          {item.description && <Text size="xs" c="dimmed" truncate="end">{item.description}</Text>}
        </Box>
      </Group>
    </UnstyledButton>
    </li>
  );

  return (
    <Modal
      opened={opened}
      onClose={close}
      title={open && !query ? open.title : 'What should happen next?'}
      size="lg"
      radius="md"
      styles={{ title: { fontWeight: 700, fontSize: 'var(--mantine-font-size-lg)' } }}
    >
      <Stack gap="md">
        <Group gap="xs" wrap="nowrap">
          {open && !query && (
            <ActionIcon variant="subtle" color="gray" aria-label="Back to the choices" onClick={() => setOpen(null)}><IconChevronLeft size="1rem" /></ActionIcon>
          )}
          <TextInput
            type="search"
            aria-label="Search steps"
            placeholder="Search steps: kafka, filter, lookup, slack…"
            leftSection={<IconSearch size="1rem" />}
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            rightSection={query ? <ActionIcon variant="subtle" color="gray" size="sm" aria-label="Clear search" onClick={() => setQuery('')}><IconX size="0.8rem" /></ActionIcon> : null}
            style={{ flex: 1 }}
            data-autofocus
          />
        </Group>

        {query ? (
          matches.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="lg">Nothing matches “{query}”.</Text>
          ) : (
            <Box component="ul" aria-label="Matching steps" m={0} p={0} style={{ listStyle: 'none', maxHeight: 420, overflowY: 'auto' }}>
              {matches.map(({ choice, item }, i) => (
                locked(choice)
                  ? <Box key={`${item.type}-${item.subType}-${item.refId}-${i}`} component="li" px="sm" py={8}><Text size="sm" c="dimmed">{item.label} · {choice.title}: add a source first</Text></Box>
                  : row(item, `${item.type}-${item.subType}-${item.refId}-${i}`)
              ))}
            </Box>
          )
        ) : open ? (
          <Box component="ul" aria-label={open.title} m={0} p={0} style={{ listStyle: 'none', maxHeight: 420, overflowY: 'auto' }}>
            {open.items.map((item, i) => row(item, `${item.type}-${item.subType}-${item.refId}-${i}`))}
          </Box>
        ) : (
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
            {choices.map((choice) => {
              const off = locked(choice);
              const names = choice.items.slice(0, 4).map((i) => i.label).join(', ');
              const more = choice.items.length - 4;
              return (
                <UnstyledButton
                  key={choice.key}
                  onClick={() => setOpen(choice)}
                  disabled={off}
                  p="md"
                  style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 10, opacity: off ? 0.55 : 1, display: 'block' }}
                  className="hermod-pick"
                >
                  <Text fw={600}>{choice.title}</Text>
                  <Text size="xs" c="dimmed" mt={2}>{off ? 'Add a source first' : choice.hint}</Text>
                  {!off && <Text size="xs" c="dimmed" mt={6}>{names}{more > 0 ? ` and ${more} more` : ''}</Text>}
                </UnstyledButton>
              );
            })}
          </SimpleGrid>
        )}

        <Group justify="space-between">
          <Text size="xs" c="dimmed">Drag from the palette if you prefer.</Text>
          <Button variant="subtle" size="xs" onClick={() => { close(); onOpenPalette(); }}>Browse the full palette</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
