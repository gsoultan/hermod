import { useState, useMemo, type ReactNode } from 'react';
import { 
  Modal, Button, Stack, TextInput, Textarea, Group, 
  Text, Paper, Box, 
  Divider, ScrollArea, ActionIcon,
  SimpleGrid, Select, Checkbox, NumberInput,
  Grid, Badge, TagsInput, Code,
  Radio, MultiSelect, Slider, Tabs, Tooltip,
  ThemeIcon, Title, Alert, Table, FileInput, Image
} from '@mantine/core';
import { 
  DndContext, 
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  DragOverlay,
  defaultDropAnimationSideEffects
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  useSortable,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { notifications } from '@mantine/notifications';
import { IconAdjustments, IconArrowAutofitWidth, IconCalendar, IconChecklist, IconCopy, IconDeviceFloppy, IconEye, IconGripVertical, IconHash, IconHeading, IconInfoCircle, IconLayout, IconLayoutRows, IconLetterT, IconList, IconListCheck, IconMail, IconPhoto, IconPlus, IconSeparator, IconSettings, IconTrash } from '@tabler/icons-react';
// --- Types ---

export type FormFieldType = 
  | 'text' | 'number' | 'date' | 'datetime' | 'image' 
  | 'multiple' | 'one' | 'email' | 'date_range' | 'scale'
  | 'table'
  | 'heading' | 'text_block' | 'divider' | 'page_break';

export interface FormFieldItem {
  id: string;
  type: FormFieldType;
  name?: string;
  label?: string;
  required?: boolean;
  options?: string[];
  placeholder?: string;
  help?: string;
  number_kind?: 'integer' | 'float';
  render?: 'select' | 'radio';
  verify_email?: boolean;
  reject_if_invalid?: boolean;
  min?: number;
  max?: number;
  step?: number;
  start_label?: string;
  end_label?: string;
  section?: string;
  width?: 'auto' | 'half' | 'full';
  // Advanced layout (overrides width when set)
  colSpan?: number; // 1..12
  // Conditional visibility
  dependsOn?: string; // name of the field this depends on
  operator?: 'eq' | 'ne' | 'gt' | 'lt' | 'includes';
  value?: string | number | boolean;
  // Table configuration (for type === 'table')
  table?: {
    bordered?: boolean;
    columns: Array<{ 
      key: string; 
      label: string; 
      type?: 'text' | 'number' | 'date' | 'datetime' | 'timestamp' | 'select' | 'one' | 'multiple';
      options?: string[]; // for select/one/multiple
    }>;
  };
  content?: string;
  level?: 1 | 2 | 3;
}

interface FormLayoutBuilderProps {
  opened: boolean;
  onClose: () => void;
  onApply: (fields: FormFieldItem[], title?: string, description?: string) => void;
  initialFields?: FormFieldItem[];
  initialTitle?: string;
  initialDescription?: string;
}

// --- Sortable Item ---

function SortableFieldItem({ 
  field, 
  onRemove, 
  onEdit, 
  isActive,
  isOverlay = false
}: { 
  field: FormFieldItem; 
  onRemove?: () => void; 
  onEdit?: () => void;
  isActive?: boolean;
  isOverlay?: boolean;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging
  } = useSortable({ id: field.id, disabled: isOverlay });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging && !isOverlay ? 0 : 1,
    zIndex: isDragging ? 1000 : 1,
  };

  const isLayoutOnly = ['heading', 'text_block', 'divider', 'page_break'].includes(field.type);
  const isHalf = field.width === 'half';

  const getIcon = () => {
    switch (field.type) {
      case 'text': return <IconLetterT size="1.2rem" />;
      case 'number': return <IconHash size="1.2rem" />;
      case 'date': 
      case 'datetime': 
      case 'date_range': return <IconCalendar size="1.2rem" />;
      case 'image': return <IconPhoto size="1.2rem" />;
      case 'multiple': return <IconListCheck size="1.2rem" />;
      case 'one': return <IconList size="1.2rem" />;
      case 'email': return <IconMail size="1.2rem" />;
      case 'scale': return <IconArrowAutofitWidth size="1.2rem" />;
      case 'table': return <IconLayout size="1.2rem" />;
      case 'heading': return <IconHeading size="1.2rem" />;
      case 'text_block': return <IconLetterT size="1.2rem" />;
      case 'divider': return <IconSeparator size="1.2rem" />;
      case 'page_break': return <IconLayoutRows size="1.2rem" />;
      default: return <IconLetterT size="1.2rem" />;
    }
  };

  return (
    <Box 
      ref={setNodeRef} 
      style={{
        ...style,
        width: isHalf && !isOverlay ? 'calc(50% - 6px)' : '100%',
        display: 'inline-block',
        margin: '3px',
        verticalAlign: 'top'
      }}
      onClick={onEdit}
    >
      <Paper
        withBorder
        p="sm"
        radius="md"
        shadow={isActive ? 'sm' : 'none'}
        style={{
          cursor: isOverlay ? 'grabbing' : 'pointer',
          border: isActive ? '2px solid var(--mantine-color-blue-filled)' : isDragging ? '2px dashed var(--mantine-color-gray-4)' : '1px solid var(--mantine-color-gray-3)',
          transition: 'all 0.2s ease',
          backgroundColor: isActive ? 'var(--mantine-color-blue-0)' : 'white',
          position: 'relative'
        }}
      >
        <Group justify="space-between" wrap="nowrap">
          <Group gap="sm" flex={1}>
            <Box {...attributes} {...listeners} style={{ cursor: isOverlay ? 'grabbing' : 'grab' }} onClick={(e) => e.stopPropagation()}>
              <IconGripVertical size="1.2rem" color="var(--mantine-color-gray-5)" />
            </Box>
            <ThemeIcon variant="light" color={isLayoutOnly ? 'gray' : 'blue'} radius="md">
              {getIcon()}
            </ThemeIcon>
            <Box flex={1}>
              {isLayoutOnly ? (
                <Stack gap={2}>
                  <Text size="xs" fw={700} c="dimmed" style={{ textTransform: 'uppercase' }}>{field.type.replace('_', ' ')}</Text>
                  <Text size="sm" fw={600} lineClamp={1}>
                    {field.type === 'heading' ? field.content || 'Heading' : 
                    field.type === 'text_block' ? field.content || 'Text content' :
                    field.type === 'divider' ? 'Separator line' : 'New page starts here'}
                  </Text>
                </Stack>
              ) : (
                <Stack gap={2}>
                  <Group gap={6} align="center">
                    <Text size="sm" fw={600}>{field.label || field.name}</Text>
                    {field.required && <Badge size="xs" color="red" variant="filled">Required</Badge>}
                  </Group>
                  <Group gap={6}>
                    <Code style={{ fontSize: '10px' }}>{field.name}</Code>
                    <Badge size="xs" variant="outline" color="gray">{field.type}</Badge>
                  </Group>
                </Stack>
              )}
            </Box>
          </Group>
          
          {!isOverlay && (
            <Group gap={4}>
              <Tooltip label="Remove Field">
                <ActionIcon 
                  variant="subtle" 
                  size="sm" 
                  onClick={(e) => { e.stopPropagation(); onRemove?.(); }} 
                  color="red"
                >
                  <IconTrash size="1rem" />
                </ActionIcon>
              </Tooltip>
            </Group>
          )}
        </Group>
      </Paper>
    </Box>
  );
}

// --- Main Builder ---

export function FormLayoutBuilder({ 
  opened, 
  onClose, 
  onApply, 
  initialFields = [],
  initialTitle = 'Untitled Form',
  initialDescription = 'Form description and instructions go here...'
}: FormLayoutBuilderProps) {
  const [fields, setFields] = useState<FormFieldItem[]>(initialFields);
  const [formTitle, setFormTitle] = useState(initialTitle);
  const [formDescription, setFormDescription] = useState(initialDescription);
  const [selectedFieldId, setSelectedFieldId] = useState<string | null>(null);
  const [previewOpened, setPreviewOpened] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [isEditingHeader, setIsEditingHeader] = useState(false);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

  const handleDragStart = (event: any) => {
    setActiveId(event.active.id);
  };

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    setActiveId(null);
    if (over && active.id !== over.id) {
      setFields((items) => {
        const oldIndex = items.findIndex((i) => i.id === active.id);
        const newIndex = items.findIndex((i) => i.id === over.id);
        return arrayMove(items, oldIndex, newIndex);
      });
    }
  };

  const addField = (type: FormFieldType) => {
    const id = Math.random().toString(36).substring(2, 11);
    const newField: FormFieldItem = {
      id,
      type,
      name: type === 'heading' || type === 'text_block' || type === 'divider' || type === 'page_break' ? undefined : `field_${id}`,
      label: type === 'heading' || type === 'text_block' || type === 'divider' || type === 'page_break' ? undefined : `New ${type.charAt(0).toUpperCase() + type.slice(1)}`,
      required: false,
      width: 'full',
      level: type === 'heading' ? 2 : undefined,
      content: type === 'text_block' ? 'Enter text here...' : (type === 'heading' ? 'Heading' : undefined),
    };
    setFields([...fields, newField]);
    setSelectedFieldId(id);
    setIsEditingHeader(false);
  };

  const updateField = (id: string, updates: Partial<FormFieldItem>) => {
    setFields(fields.map(f => f.id === id ? { ...f, ...updates } : f));
  };

  const removeField = (id: string) => {
    setFields(fields.filter(f => f.id !== id));
    if (selectedFieldId === id) setSelectedFieldId(null);
  };

  const selectedField = useMemo(() => fields.find(f => f.id === selectedFieldId), [fields, selectedFieldId]);
  const activeField = useMemo(() => fields.find(f => f.id === activeId), [fields, activeId]);

  const isLayoutOnly = (t: FormFieldType) => ['heading', 'text_block', 'divider', 'page_break'].includes(t);

  const paletteItem = (type: FormFieldType, label: string, icon: ReactNode, variant: "light" | "outline" = "light") => (
    <Tooltip label={`Add ${label}`} position="right" withArrow>
      <Button 
        variant={variant} 
        size="compact-xs" 
        leftSection={icon} 
        onClick={() => addField(type)}
        styles={{
          inner: { justifyContent: 'flex-start' },
          root: { borderStyle: variant === 'outline' ? 'dashed' : 'solid' }
        }}
        fullWidth
      >
        {label}
      </Button>
    </Tooltip>
  );

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={<Group gap="xs"><IconLayout size="1.2rem" /><Text fw={700}>Professional Form Builder</Text></Group>}
      size="90%"
      fullScreen
      padding={0}
      styles={{
        header: { backgroundColor: 'var(--mantine-color-gray-0)', borderBottom: '1px solid var(--mantine-color-gray-3)', margin: 0, padding: '10px 20px' },
        body: { padding: 0 }
      }}
    >
      <Stack h="calc(100vh - 54px)" gap={0}>
        <Group h={50} px="md" justify="space-between" bg="white" style={{ borderBottom: '1px solid var(--mantine-color-gray-2)' }}>
          <Group gap="xs">
            <Button 
              leftSection={<IconEye size="1rem" />} 
              variant="outline" 
              size="xs"
              onClick={() => setPreviewOpened(true)}
            >
              Live Preview
            </Button>
            <Divider orientation="vertical" />
            <Button 
              leftSection={<IconCopy size="1rem" />} 
              variant="subtle" 
              size="xs"
              color="gray"
              onClick={() => {
                navigator.clipboard.writeText(JSON.stringify(fields, null, 2));
                notifications.show({ title: 'Copied', message: 'Layout JSON copied to clipboard', color: 'blue' });
              }}
            >
              Export JSON
            </Button>
          </Group>
          <Group gap="xs">
            <Button variant="subtle" color="gray" onClick={onClose} size="xs">Cancel</Button>
            <Button leftSection={<IconDeviceFloppy size="1rem" />} onClick={() => onApply(fields, formTitle, formDescription)} size="xs">Save Form Layout</Button>
          </Group>
        </Group>

        <Grid flex={1} m={0} gutter={0}>
          <Grid.Col span={2} style={{ borderRight: '1px solid var(--mantine-color-gray-2)' }} bg="gray.0">
            <ScrollArea h="calc(100vh - 104px)" p="md">
              <Stack gap="lg">
                <Box>
                  <Text fw={700} size="xs" c="dimmed" mb="xs" style={{ letterSpacing: '0.5px' }}>INPUT ELEMENTS</Text>
                  <SimpleGrid cols={1} spacing="xs">
                    {paletteItem('text', 'Short Text', <IconLetterT size="0.9rem" />)}
                    {paletteItem('number', 'Number', <IconHash size="0.9rem" />)}
                    {paletteItem('email', 'Email Address', <IconMail size="0.9rem" />)}
                    {paletteItem('date', 'Date Picker', <IconCalendar size="0.9rem" />)}
                    {paletteItem('one', 'Single Choice', <IconList size="0.9rem" />)}
                    {paletteItem('multiple', 'Multiple Choice', <IconListCheck size="0.9rem" />)}
                    {paletteItem('image', 'File Upload', <IconPhoto size="0.9rem" />)}
                    {paletteItem('scale', 'Linear Scale', <IconArrowAutofitWidth size="0.9rem" />)}
                    {paletteItem('table', 'Table (rows x columns)', <IconLayout size="0.9rem" />)}
                  </SimpleGrid>
                </Box>

                <Box>
                  <Text fw={700} size="xs" c="dimmed" mb="xs" style={{ letterSpacing: '0.5px' }}>LAYOUT & DESIGN</Text>
                  <SimpleGrid cols={1} spacing="xs">
                    {paletteItem('heading', 'Section Heading', <IconHeading size="0.9rem" />, "outline")}
                    {paletteItem('text_block', 'Description Text', <IconLetterT size="0.9rem" />, "outline")}
                    {paletteItem('divider', 'Separator line', <IconSeparator size="0.9rem" />, "outline")}
                    {paletteItem('page_break', 'Next Page', <IconLayoutRows size="0.9rem" />, "outline")}
                  </SimpleGrid>
                </Box>

                <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light" p="xs">
                  <Text size="xs">Drag elements to reorder or click to configure.</Text>
                </Alert>
              </Stack>
            </ScrollArea>
          </Grid.Col>

          <Grid.Col span={7} bg="gray.1" style={{ position: 'relative' }}>
            <ScrollArea h="calc(100vh - 104px)" p="xl">
              <Box maw={700} mx="auto">
                <Paper shadow="md" radius="md" p={0} withBorder mih={600} style={{ backgroundColor: 'white', overflow: 'hidden' }}>
                  {/* Form Header Decoration */}
                  <Box h={8} bg="blue" />
                  <Box 
                    p="xl" 
                    style={{ 
                      borderBottom: '1px solid var(--mantine-color-gray-2)',
                      cursor: 'pointer',
                      backgroundColor: isEditingHeader ? 'var(--mantine-color-blue-0)' : 'transparent',
                      transition: 'background-color 0.2s'
                    }}
                    onClick={() => {
                      setSelectedFieldId(null);
                      setIsEditingHeader(true);
                    }}
                  >
                    {isEditingHeader ? (
                      <Stack gap="xs">
                        <TextInput 
                          label="Form Title" 
                          value={formTitle} 
                          onChange={(e) => setFormTitle(e.target.value)} 
                        />
                        <Textarea 
                          label="Form Description" 
                          value={formDescription} 
                          onChange={(e) => setFormDescription(e.target.value)} 
                        />
                        <Button size="xs" variant="light" onClick={(e) => { e.stopPropagation(); setIsEditingHeader(false); }}>Done Editing Header</Button>
                      </Stack>
                    ) : (
                      <>
                        <Title order={3}>{formTitle}</Title>
                        <Text size="sm" c="dimmed">{formDescription}</Text>
                      </>
                    )}
                  </Box>

                  <Box p="xl">
                    <DndContext 
                      sensors={sensors}
                      collisionDetection={closestCenter}
                      onDragStart={handleDragStart}
                      onDragEnd={handleDragEnd}
                    >
                      <SortableContext 
                        items={fields.map(f => f.id)}
                        strategy={verticalListSortingStrategy}
                      >
                        {fields.length === 0 ? (
                          <Stack align="center" justify="center" h={300} gap="sm" style={{ border: '2px dashed var(--mantine-color-gray-3)', borderRadius: '12px' }}>
                            <ThemeIcon size={60} radius={60} variant="light" color="gray">
                              <IconPlus size="2rem" />
                            </ThemeIcon>
                            <Text fw={600} c="dimmed">Your form is empty</Text>
                            <Text size="xs" c="dimmed">Add elements from the sidebar to start building</Text>
                          </Stack>
                        ) : (
                          <Box style={{ fontSize: 0 }}>
                            {fields.map((field) => (
                              <SortableFieldItem 
                                key={field.id} 
                                field={field} 
                                onRemove={() => removeField(field.id)}
                                onEdit={() => setSelectedFieldId(field.id)}
                                isActive={selectedFieldId === field.id}
                              />
                            ))}
                          </Box>
                        )}
                      </SortableContext>
                      
                      <DragOverlay dropAnimation={{
                        sideEffects: defaultDropAnimationSideEffects({
                          styles: {
                            active: {
                              opacity: '0.5',
                            },
                          },
                        }),
                      }}>
                        {activeId && activeField ? (
                          <SortableFieldItem field={activeField} isActive isOverlay />
                        ) : null}
                      </DragOverlay>
                    </DndContext>
                  </Box>
                </Paper>
                <Text ta="center" size="xs" c="dimmed" mt="md">End of Form</Text>
              </Box>
            </ScrollArea>
          </Grid.Col>

          <Grid.Col span={3} style={{ borderLeft: '1px solid var(--mantine-color-gray-2)' }} bg="white">
            <ScrollArea h="calc(100vh - 104px)">
              {isEditingHeader ? (
                <Stack p="md" gap="md">
                  <Text fw={700} size="sm">Form Header Settings</Text>
                  <TextInput 
                    label="Form Title" 
                    value={formTitle} 
                    onChange={(e) => setFormTitle(e.target.value)} 
                  />
                  <Textarea 
                    label="Form Description" 
                    value={formDescription} 
                    onChange={(e) => setFormDescription(e.target.value)} 
                    rows={4}
                  />
                  <Button onClick={() => setIsEditingHeader(false)}>Back to Elements</Button>
                </Stack>
              ) : selectedField ? (
                <Tabs defaultValue="basic">
                  <Tabs.List grow>
                    <Tabs.Tab value="basic" leftSection={<IconAdjustments size="1rem" />}>Basic</Tabs.Tab>
                    <Tabs.Tab value="validation" leftSection={<IconChecklist size="1rem" />}>Validation</Tabs.Tab>
                    <Tabs.Tab value="layout" leftSection={<IconLayout size="1rem" />}>Layout</Tabs.Tab>
                  </Tabs.List>

                  <Tabs.Panel value="basic" p="md">
                    <Stack gap="md">
                      <Group justify="space-between">
                        <Text fw={700} size="sm">General Settings</Text>
                        <Badge variant="light" color="blue" size="sm">{selectedField.type}</Badge>
                      </Group>

                      {!isLayoutOnly(selectedField.type) && (
                        <>
                          <TextInput 
                            label="Field Label" 
                            description="The name shown to the user"
                            value={selectedField.label || ''} 
                            onChange={(e) => updateField(selectedField.id, { label: e.target.value })} 
                          />
                          <TextInput 
                            label="Internal Name (ID)" 
                            description="Key name in the submitted data"
                            value={selectedField.name || ''} 
                            onChange={(e) => updateField(selectedField.id, { name: e.target.value })} 
                          />
                          <TextInput 
                            label="Placeholder" 
                            placeholder="e.g. Enter your name"
                            value={selectedField.placeholder || ''} 
                            onChange={(e) => updateField(selectedField.id, { placeholder: e.target.value })} 
                          />
                          <Textarea 
                            label="Help Text" 
                            description="Additional instructions for the user"
                            value={selectedField.help || ''} 
                            onChange={(e) => updateField(selectedField.id, { help: e.target.value })} 
                            rows={2}
                          />
                        </>
                      )}

                      {selectedField.type === 'heading' && (
                        <>
                          <TextInput 
                            label="Heading Text" 
                            value={selectedField.content || ''} 
                            onChange={(e) => updateField(selectedField.id, { content: e.target.value })} 
                          />
                          <Select 
                            label="Heading Level" 
                            data={[{ value: '1', label: 'H1 - Large' }, { value: '2', label: 'H2 - Medium' }, { value: '3', label: 'H3 - Small' }]} 
                            value={String(selectedField.level || 2)} 
                            onChange={(val) => updateField(selectedField.id, { level: (val ? Number(val) : 2) as 1|2|3 })} 
                          />
                        </>
                      )}

                      {selectedField.type === 'text_block' && (
                        <Textarea 
                          label="Paragraph Content" 
                          value={selectedField.content || ''} 
                          onChange={(e) => updateField(selectedField.id, { content: e.target.value })} 
                          rows={6}
                        />
                      )}

                      {(selectedField.type === 'one' || selectedField.type === 'multiple') && (
                        <>
                          <TagsInput 
                            label="Options" 
                            description="Enter options and press Enter"
                            placeholder="Option 1" 
                            value={selectedField.options || []} 
                            onChange={(val) => updateField(selectedField.id, { options: val })} 
                          />
                          {selectedField.type === 'one' && (
                            <Select 
                              label="Display Mode" 
                              data={[{ value: 'select', label: 'Dropdown List' }, { value: 'radio', label: 'Radio Buttons' }]} 
                              value={selectedField.render || 'select'} 
                              onChange={(val) => updateField(selectedField.id, { render: (val as any) || 'select' })} 
                            />
                          )}
                        </>
                      )}

                      {selectedField.type === 'table' && (
                        <Stack gap="sm">
                          <Group justify="space-between">
                            <Text fw={600} size="sm">Table Columns</Text>
                            <Button size="compact-xs" variant="light" onClick={() => {
                              const cols = selectedField.table?.columns || [];
                              const id = Math.random().toString(36).slice(2,7);
                              updateField(selectedField.id, { table: { bordered: selectedField.table?.bordered, columns: [...cols, { key: `col_${id}`, label: 'Column', type: 'text' }] } });
                            }}>Add Column</Button>
                          </Group>
                          <Stack gap={6}>
                            {(selectedField.table?.columns || []).map((c, idx) => (
                              <Group key={idx} align="flex-end" gap="xs">
                                <TextInput label="Key" value={c.key} onChange={(e) => {
                                  const cols = [...(selectedField.table?.columns || [])];
                                  cols[idx] = { ...cols[idx], key: e.target.value };
                                  updateField(selectedField.id, { table: { bordered: selectedField.table?.bordered, columns: cols } });
                                }} />
                                <TextInput label="Label" value={c.label} onChange={(e) => {
                                  const cols = [...(selectedField.table?.columns || [])];
                                  cols[idx] = { ...cols[idx], label: e.target.value };
                                  updateField(selectedField.id, { table: { bordered: selectedField.table?.bordered, columns: cols } });
                                }} />
                                <Select label="Type" data={[ 'text','number','date','datetime','timestamp','select','one','multiple' ]} value={c.type || 'text'} onChange={(val) => {
                                  const cols = [...(selectedField.table?.columns || [])];
                                  cols[idx] = { ...cols[idx], type: (val as any) || 'text' };
                                  updateField(selectedField.id, { table: { bordered: selectedField.table?.bordered, columns: cols } });
                                }} />
                                {(c.type === 'select' || c.type === 'one' || c.type === 'multiple') && (
                                  <TagsInput 
                                    label="Options" 
                                    placeholder="Add option and press Enter"
                                    value={c.options || []}
                                    onChange={(val) => {
                                      const cols = [...(selectedField.table?.columns || [])];
                                      cols[idx] = { ...cols[idx], options: val } as any;
                                      updateField(selectedField.id, { table: { bordered: selectedField.table?.bordered, columns: cols } });
                                    }}
                                  />
                                )}
                                <ActionIcon color="red" variant="light" onClick={() => {
                                  const cols = (selectedField.table?.columns || []).filter((_, i) => i !== idx);
                                  updateField(selectedField.id, { table: { bordered: selectedField.table?.bordered, columns: cols } });
                                }}>
                                  <IconTrash size="1rem" />
                                </ActionIcon>
                              </Group>
                            ))}
                          </Stack>
                          <Checkbox 
                            label="Bordered Table"
                            checked={!!selectedField.table?.bordered}
                            onChange={(e) => updateField(selectedField.id, { table: { bordered: e.currentTarget.checked, columns: selectedField.table?.columns || [] } })}
                          />
                        </Stack>
                      )}
                    </Stack>
                  </Tabs.Panel>

                  <Tabs.Panel value="validation" p="md">
                    <Stack gap="md">
                      <Text fw={700} size="sm">Rules & Constraints</Text>
                      
                      {!isLayoutOnly(selectedField.type) && (
                        <Checkbox 
                          label="Required Field" 
                          description="User must fill this field before submitting"
                          checked={!!selectedField.required} 
                          onChange={(e) => updateField(selectedField.id, { required: e.currentTarget.checked })} 
                        />
                      )}

                      {selectedField.type === 'email' && (
                        <Checkbox 
                          label="Strict Email Verification" 
                          description="Check if the email domain exists"
                          checked={!!selectedField.verify_email}
                          onChange={(e) => updateField(selectedField.id, { verify_email: e.currentTarget.checked })}
                        />
                      )}

                      {selectedField.type === 'scale' && (
                        <Stack gap="xs">
                          <Group grow>
                            <NumberInput 
                              label="Min Value" 
                              value={selectedField.min} 
                              onChange={(val) => updateField(selectedField.id, { min: Number(val) })} 
                            />
                            <NumberInput 
                              label="Max Value" 
                              value={selectedField.max} 
                              onChange={(val) => updateField(selectedField.id, { max: Number(val) })} 
                            />
                          </Group>
                          <NumberInput 
                            label="Step" 
                            value={selectedField.step} 
                            onChange={(val) => updateField(selectedField.id, { step: Number(val) })} 
                          />
                        </Stack>
                      )}

                      {selectedField.type === 'number' && (
                        <Select 
                          label="Allow Values" 
                          data={[{ value: 'integer', label: 'Integers Only' }, { value: 'float', label: 'Decimals Allowed' }]} 
                          value={selectedField.number_kind || 'float'} 
                          onChange={(val) => updateField(selectedField.id, { number_kind: (val as any) || 'float' })} 
                        />
                      )}

                      {!isLayoutOnly(selectedField.type) && (
                        <>
                          <Divider label="Conditional Visibility" labelPosition="left" my="xs" />
                          <Select 
                            label="Depends On Field"
                            placeholder="Select field"
                            data={fields.filter(f => f.id !== selectedField.id && !isLayoutOnly(f.type) && f.name).map(f => ({ value: f.name!, label: f.label || f.name! }))}
                            value={selectedField.dependsOn || null as any}
                            onChange={(val) => updateField(selectedField.id, { dependsOn: (val as string) || undefined })}
                            clearable
                          />
                          <Group grow>
                            <Select 
                              label="Operator"
                              data={[
                                { value: 'eq', label: 'equals' },
                                { value: 'ne', label: 'not equals' },
                                { value: 'includes', label: 'includes (array)' },
                                { value: 'gt', label: 'greater than' },
                                { value: 'lt', label: 'less than' }
                              ]}
                              value={selectedField.operator || 'eq'}
                              onChange={(val) => updateField(selectedField.id, { operator: (val as any) || 'eq' })}
                            />
                            <TextInput 
                              label="Compare To"
                              placeholder="value"
                              value={selectedField.value as any || ''}
                              onChange={(e) => updateField(selectedField.id, { value: e.target.value })}
                            />
                          </Group>
                        </>
                      )}
                    </Stack>
                  </Tabs.Panel>

                  <Tabs.Panel value="layout" p="md">
                    <Stack gap="md">
                      <Text fw={700} size="sm">Appearance</Text>
                      
                      <Select 
                        label="Column Width" 
                        description="How much horizontal space this field occupies"
                        data={[
                          { value: 'auto', label: 'Adaptive (Auto)' },
                          { value: 'half', label: 'Half Width (50%)' },
                          { value: 'full', label: 'Full Width (100%)' }
                        ]} 
                        value={selectedField.width || 'full'} 
                        onChange={(val) => updateField(selectedField.id, { width: (val as any) || 'full' })} 
                      />

                      <NumberInput 
                        label="Grid Columns (1–12)"
                        description="Overrides width when set"
                        value={selectedField.colSpan}
                        min={1}
                        max={12}
                        onChange={(val) => updateField(selectedField.id, { colSpan: Number(val) || undefined })}
                      />

                      {selectedField.type === 'date_range' && (
                        <Stack gap="xs">
                           <TextInput 
                            label="Start Input Label" 
                            value={selectedField.start_label || ''} 
                            onChange={(e) => updateField(selectedField.id, { start_label: e.target.value })} 
                          />
                          <TextInput 
                            label="End Input Label" 
                            value={selectedField.end_label || ''} 
                            onChange={(e) => updateField(selectedField.id, { end_label: e.target.value })} 
                          />
                        </Stack>
                      )}

                      <TextInput 
                        label="Section Name" 
                        placeholder="e.g. Personal Information" 
                        description="Group fields visually into sections"
                        value={selectedField.section || ''} 
                        onChange={(e) => updateField(selectedField.id, { section: e.target.value })} 
                      />
                    </Stack>
                  </Tabs.Panel>
                </Tabs>
              ) : (
                <Stack align="center" justify="center" h={400} gap="sm" p="xl">
                  <ThemeIcon size={40} radius="xl" variant="light" color="gray">
                    <IconSettings size="1.2rem" />
                  </ThemeIcon>
                  <Text size="sm" fw={600} c="dimmed">No element selected</Text>
                  <Text size="xs" c="dimmed" ta="center">Click on an element in the form to edit its properties.</Text>
                </Stack>
              )}
            </ScrollArea>
          </Grid.Col>
        </Grid>
      </Stack>
      <FormPreview 
        opened={previewOpened} 
        onClose={() => setPreviewOpened(false)} 
        fields={fields} 
        title={formTitle}
        description={formDescription}
      />
    </Modal>
  );
}

export function FormPreview({ opened, onClose, fields, title, description }: { opened: boolean; onClose: () => void; fields: FormFieldItem[]; title?: string; description?: string }) {
  const [currentPage, setCurrentPage] = useState(0);
  const [values, setValues] = useState<Record<string, any>>({});
  const [rowsByField, setRowsByField] = useState<Record<string, any[]>>({});

  const pages = useMemo(() => {
    const p: FormFieldItem[][] = [[]];
    let cur = 0;
    fields.forEach(f => {
      if (f.type === 'page_break') {
        p.push([]);
        cur++;
      } else {
        p[cur].push(f);
      }
    });
    return p;
  }, [fields]);

  const isVisible = (f: FormFieldItem) => {
    if (!f.dependsOn) return true;
    const cur = values[f.dependsOn];
    const op = f.operator || 'eq';
    const cmp = f.value;
    switch (op) {
      case 'eq': return String(cur) === String(cmp);
      case 'ne': return String(cur) !== String(cmp);
      case 'gt': return Number(cur) > Number(cmp);
      case 'lt': return Number(cur) < Number(cmp);
      case 'includes': return Array.isArray(cur) && (cmp !== undefined) ? cur.includes(cmp) : false;
      default: return true;
    }
  };

  const renderField = (f: FormFieldItem) => {
    if (!isVisible(f)) return null;
    const isHalf = f.width === 'half';
    const gridSpan = f.colSpan ? Math.min(12, Math.max(1, f.colSpan)) : (isHalf ? 6 : 12);

    const commonProps = {
      label: f.label || f.name,
      description: f.help,
      placeholder: f.placeholder,
      required: f.required,
    };

    let content;
    switch (f.type) {
      case 'heading':
        content = (
          <Box pt="md" pb="xs">
            <Text fw={700} size={f.level === 1 ? 'xl' : f.level === 3 ? 'sm' : 'md'}>
              {f.content}
            </Text>
          </Box>
        );
        break;
      case 'text_block':
        content = <Text size="sm" py="xs">{f.content}</Text>;
        break;
      case 'divider':
        content = <Divider my="lg" />;
        break;
      case 'text':
        content = <TextInput {...commonProps} value={values[f.name || ''] || ''} onChange={(e) => setValues(v => ({ ...v, [f.name!]: e.currentTarget.value }))} />;
        break;
      case 'number':
        content = <NumberInput {...commonProps} step={f.number_kind === 'integer' ? 1 : 0.1} value={values[f.name || ''] ?? undefined} onChange={(val) => setValues(v => ({ ...v, [f.name!]: val }))} />;
        break;
      case 'email':
        content = <TextInput {...commonProps} type="email" value={values[f.name || ''] || ''} onChange={(e) => setValues(v => ({ ...v, [f.name!]: e.currentTarget.value }))} />;
        break;
      case 'date':
        content = <TextInput {...commonProps} type="date" value={values[f.name || ''] || ''} onChange={(e) => setValues(v => ({ ...v, [f.name!]: e.currentTarget.value }))} />;
        break;
      case 'datetime':
        content = <TextInput {...commonProps} type="datetime-local" value={values[f.name || ''] || ''} onChange={(e) => setValues(v => ({ ...v, [f.name!]: e.currentTarget.value }))} />;
        break;
      case 'date_range':
        content = (
          <Stack gap="xs">
            <Text size="sm" fw={500}>{f.label || f.name}{f.required && <Text span c="red"> *</Text>}</Text>
            <Group grow>
              <TextInput label={f.start_label || 'Start'} type="date" />
              <TextInput label={f.end_label || 'End'} type="date" />
            </Group>
            {f.help && <Text size="xs" c="dimmed">{f.help}</Text>}
          </Stack>
        );
        break;
      case 'image':
        content = (
          <Stack gap="xs">
            <Group justify="space-between" align="flex-end">
              <Text size="sm" fw={500}>{f.label || f.name}{f.required && <Text span c="red"> *</Text>}</Text>
            </Group>
            <FileInput 
              accept="image/*" 
              placeholder="Choose image..."
              value={values[f.name || ''] || null}
              onChange={(file) => setValues(v => ({ ...v, [f.name!]: file }))}
            />
            {values[f.name || ''] && (
              <Image 
                src={URL.createObjectURL(values[f.name || ''] as File)}
                alt="preview"
                radius="sm"
                h={140}
                fit="contain"
              />
            )}
            {f.help && <Text size="xs" c="dimmed">{f.help}</Text>}
          </Stack>
        );
        break;
      case 'one':
        if (f.render === 'radio') {
          content = (
            <Radio.Group {...commonProps} value={values[f.name || ''] || ''} onChange={(val) => setValues(v => ({ ...v, [f.name!]: val }))}>
              <Stack gap="xs" mt="xs">
                {(f.options || []).map(opt => <Radio key={opt} value={opt} label={opt} />)}
              </Stack>
            </Radio.Group>
          );
        } else {
          content = <Select {...commonProps} data={f.options || []} value={values[f.name || ''] || null as any} onChange={(val) => setValues(v => ({ ...v, [f.name!]: val }))} />;
        }
        break;
      case 'multiple':
        content = <MultiSelect {...commonProps} data={f.options || []} value={(values[f.name || ''] as any) || []} onChange={(val) => setValues(v => ({ ...v, [f.name!]: val }))} />;
        break;
      case 'scale':
        content = (
          <Stack gap="xs">
            <Text size="sm" fw={500}>{f.label || f.name}{f.required && <Text span c="red"> *</Text>}</Text>
            <Slider 
              min={f.min} 
              max={f.max} 
              step={f.step} 
              marks={[
                { value: f.min || 0, label: String(f.min || 0) },
                { value: f.max || 100, label: String(f.max || 100) }
              ]} 
              mb="xl"
            />
            {f.help && <Text size="xs" c="dimmed">{f.help}</Text>}
          </Stack>
        );
        break;
      case 'table':
        {
          const key = (f.name || f.id)!;
          const rows = rowsByField[key] || [];
          const cols = f.table?.columns || [];
          const addRow = () => {
            const empty: any = {};
            cols.forEach(c => { empty[c.key] = ''; });
            setRowsByField(s => ({ ...s, [key]: [...(s[key] || []), empty] }));
          };
          const removeRow = (idx: number) => {
            setRowsByField(s => ({ ...s, [key]: (s[key] || []).filter((_, i) => i !== idx) }));
          };
          content = (
            <Stack gap="xs">
              <Group justify="space-between" align="center">
                <Text size="sm" fw={500}>{f.label || f.name}{f.required && <Text span c="red"> *</Text>}</Text>
                <Button size="compact-xs" variant="light" onClick={addRow}>Add Row</Button>
              </Group>
              <Table withTableBorder={!!f.table?.bordered} withColumnBorders={!!f.table?.bordered} horizontalSpacing="md" verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    {cols.map(c => (<Table.Th key={c.key}>{c.label || c.key}</Table.Th>))}
                    <Table.Th style={{ width: 60 }}></Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {rows.length === 0 ? (
                    <Table.Tr>
                      <Table.Td colSpan={cols.length + 1}>
                        <Text size="xs" c="dimmed">No rows yet. Click "Add Row".</Text>
                      </Table.Td>
                    </Table.Tr>
                  ) : rows.map((r, rIdx) => (
                    <Table.Tr key={rIdx}>
                      {cols.map((c) => (
                        <Table.Td key={c.key}>
                          {c.type === 'number' ? (
                            <NumberInput size="xs" value={r[c.key] ?? undefined} onChange={(val) => {
                              setRowsByField(s => {
                                const next = [...(s[key] || [])];
                                next[rIdx] = { ...next[rIdx], [c.key]: val };
                                return { ...s, [key]: next };
                              });
                            }} />
                          ) : c.type === 'date' ? (
                            <TextInput size="xs" type="date" value={r[c.key] || ''} onChange={(e) => {
                              const val = e.currentTarget.value;
                              setRowsByField(s => {
                                const next = [...(s[key] || [])];
                                next[rIdx] = { ...next[rIdx], [c.key]: val };
                                return { ...s, [key]: next };
                              });
                            }} />
                          ) : c.type === 'datetime' || c.type === 'timestamp' ? (
                            <TextInput size="xs" type="datetime-local" value={r[c.key] || ''} onChange={(e) => {
                              const val = e.currentTarget.value;
                              setRowsByField(s => {
                                const next = [...(s[key] || [])];
                                next[rIdx] = { ...next[rIdx], [c.key]: val };
                                return { ...s, [key]: next };
                              });
                            }} />
                          ) : c.type === 'select' || c.type === 'one' ? (
                            <Select size="xs" data={c.options || []} value={r[c.key] || null as any} onChange={(val) => {
                              setRowsByField(s => {
                                const next = [...(s[key] || [])];
                                next[rIdx] = { ...next[rIdx], [c.key]: val };
                                return { ...s, [key]: next };
                              });
                            }} />
                          ) : c.type === 'multiple' ? (
                            <MultiSelect size="xs" data={c.options || []} value={Array.isArray(r[c.key]) ? r[c.key] : []} onChange={(val) => {
                              setRowsByField(s => {
                                const next = [...(s[key] || [])];
                                next[rIdx] = { ...next[rIdx], [c.key]: val };
                                return { ...s, [key]: next };
                              });
                            }} />
                          ) : (
                            <TextInput size="xs" value={r[c.key] || ''} onChange={(e) => {
                              const val = e.currentTarget.value;
                              setRowsByField(s => {
                                const next = [...(s[key] || [])];
                                next[rIdx] = { ...next[rIdx], [c.key]: val };
                                return { ...s, [key]: next };
                              });
                            }} />
                          )}
                        </Table.Td>
                      ))}
                      <Table.Td>
                        <ActionIcon color="red" variant="subtle" onClick={() => removeRow(rIdx)}>
                          <IconTrash size="1rem" />
                        </ActionIcon>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
              {f.help && <Text size="xs" c="dimmed">{f.help}</Text>}
            </Stack>
          );
        }
        break;
      default:
        content = <Text c="red">Unsupported field type: {f.type}</Text>;
    }

    return (
      <Grid.Col span={gridSpan} key={f.id}>
        {content}
      </Grid.Col>
    );
  };

  return (
    <Modal opened={opened} onClose={onClose} title="Form Preview" size="xl">
      <Paper p="xl" bg="gray.0" radius="md">
        <Paper p="xl" shadow="xs" radius="md" withBorder>
          <Box mb="xl" style={{ borderBottom: '1px solid var(--mantine-color-gray-2)', paddingBottom: '1rem' }}>
            <Title order={3}>{title || 'Untitled Form'}</Title>
            {description && <Text size="sm" c="dimmed">{description}</Text>}
          </Box>
          <Grid>
            {pages[currentPage].map(renderField)}
          </Grid>
          
          <Group justify="space-between" mt="xl">
            {pages.length > 1 && (
              <Group>
                <Button 
                  variant="default" 
                  disabled={currentPage === 0} 
                  onClick={() => setCurrentPage(p => p - 1)}
                >
                  Previous
                </Button>
                <Text size="sm">Page {currentPage + 1} of {pages.length}</Text>
                <Button 
                  variant="default" 
                  disabled={currentPage === pages.length - 1} 
                  onClick={() => setCurrentPage(p => p + 1)}
                >
                  Next
                </Button>
              </Group>
            )}
            <Button ml="auto" onClick={() => {
              const payload: Record<string, any> = { ...values };
              // attach table rows
              fields.forEach((f) => {
                if (f.type === 'table') {
                  const key = (f.name || f.id)!;
                  payload[key] = rowsByField[key] || [];
                }
              });
              // include URL query params as _params
              try {
                const params = Object.fromEntries(new URLSearchParams(window.location.search).entries());
                if (Object.keys(params).length) {
                  payload._params = params;
                }
              } catch {}
              // Serialize for preview; strip File objects to metadata to avoid errors
              const safePayload = JSON.parse(JSON.stringify(payload, (_key, v) => {
                // mark _key as used to satisfy strict TS configs
                void _key;
                if (v instanceof File) {
                  return { _file: true, name: v.name, size: v.size, type: v.type };
                }
                return v;
              }));
              // Show a small toast and log the payload
              notifications.show({
                title: 'Preview Submission',
                message: 'Open console to inspect the simulated payload.',
                color: 'blue'
              });
              // eslint-disable-next-line no-console
              console.log('Form Preview Payload:', safePayload);
            }}>Submit</Button>
          </Group>
        </Paper>
      </Paper>
    </Modal>
  );
}


