import { Group, SegmentedControl, TextInput } from '@mantine/core';
import { IconSearch } from '@tabler/icons-react';
import type { ReactNode } from 'react';

interface FilterBarProps {
  search?: { value: string; onChange: (value: string) => void; placeholder: string };
  filter?: { value: string; onChange: (value: string) => void; options: { value: string; label: string }[] };
  /** Anything else, such as a workspace picker, goes after the two. */
  children?: ReactNode;
}

/** Search plus one segmented filter, above every list. Nothing stacks above the table. */
export function FilterBar({ search, filter, children }: FilterBarProps) {
  return (
    <Group gap="sm" mb="md" wrap="wrap">
      {search && (
        <TextInput
          type="search"
          aria-label={search.placeholder}
          placeholder={search.placeholder}
          value={search.value}
          onChange={(e) => search.onChange(e.currentTarget.value)}
          leftSection={<IconSearch size="1rem" stroke={1.5} />}
          style={{ flex: '1 1 240px', maxWidth: 520 }}
        />
      )}
      {filter && (
        <SegmentedControl
          value={filter.value}
          onChange={filter.onChange}
          data={filter.options}
          radius="md"
        />
      )}
      {children}
    </Group>
  );
}
