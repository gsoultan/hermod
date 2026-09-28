import { useMemo, useState } from 'react';
import { ActionIcon, Button, Combobox, ScrollArea, Tooltip, useCombobox } from '@mantine/core';
import { IconBraces } from '@tabler/icons-react';

/** More than this is a list nobody scrolls; the search narrows it instead. */
const MAX_SHOWN = 200;

/**
 * Picks one of the node's available fields and hands back its path, so a
 * `{{ }}` token is chosen rather than typed. A mistyped path is not an error
 * anywhere in the editor: it resolves to nothing and the request goes out with
 * "" in its place, which the endpoint reports as a body it cannot read.
 */
export function InsertFieldButton({
  fieldPaths,
  onPick,
  label,
  compact = false,
}: {
  fieldPaths: string[];
  onPick: (path: string) => void;
  /** The accessible name, which also says where the token goes. */
  label: string;
  /** An icon, for inside an input; otherwise a labelled button. */
  compact?: boolean;
}) {
  const [search, setSearch] = useState('');
  const combobox = useCombobox({
    onDropdownClose: () => {
      combobox.resetSelectedOption();
      setSearch('');
    },
    onDropdownOpen: () => combobox.focusSearchInput(),
  });

  const shown = useMemo(() => {
    const query = search.trim().toLowerCase();
    const matches = query ? fieldPaths.filter((p) => p.toLowerCase().includes(query)) : fieldPaths;
    return matches.slice(0, MAX_SHOWN);
  }, [fieldPaths, search]);

  return (
    <Combobox
      store={combobox}
      width={300}
      // The icon sits at an input's right edge, the button at a toolbar's left:
      // each opens over its own field rather than over the next column.
      position={compact ? 'bottom-end' : 'bottom-start'}
      withinPortal
      onOptionSubmit={(path) => {
        onPick(path);
        combobox.closeDropdown();
      }}
    >
      <Combobox.Target withAriaAttributes={false}>
        {compact ? (
          <Tooltip label="Insert a field" withArrow>
            <ActionIcon
              aria-label={label}
              variant="subtle"
              color="gray"
              size="sm"
              onClick={() => combobox.toggleDropdown()}
            >
              <IconBraces size="0.9rem" />
            </ActionIcon>
          </Tooltip>
        ) : (
          <Button
            aria-label={label}
            size="compact-xs"
            variant="light"
            leftSection={<IconBraces size="0.8rem" />}
            onClick={() => combobox.toggleDropdown()}
          >
            Insert field
          </Button>
        )}
      </Combobox.Target>

      <Combobox.Dropdown>
        <Combobox.Search
          value={search}
          onChange={(event) => setSearch(event.currentTarget.value)}
          placeholder="Search fields"
          // Named for its job: the Available Fields panel has its own search.
          aria-label="Search fields to insert"
        />
        <Combobox.Options>
          <ScrollArea.Autosize mah={260} type="auto">
            {shown.length === 0 ? (
              <Combobox.Empty>
                {fieldPaths.length === 0 ? 'No fields yet. Refresh the sample first.' : 'No field matches.'}
              </Combobox.Empty>
            ) : (
              shown.map((path) => (
                <Combobox.Option value={path} key={path} style={{ fontFamily: 'var(--mantine-font-family-monospace)' }}>
                  {path}
                </Combobox.Option>
              ))
            )}
          </ScrollArea.Autosize>
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  );
}
