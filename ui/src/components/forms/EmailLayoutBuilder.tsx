import { useState, useEffect, useMemo, useCallback } from 'react';
import { 
  Modal, Button, Stack, TextInput, Textarea, Group, 
  ColorInput, Text, Paper, Box, 
  Divider, Center, ScrollArea, ActionIcon, Tooltip,
  SimpleGrid, NumberInput, Select, UnstyledButton,
  AppShell, rem, Code
} from '@mantine/core';
import { 
  DndContext, 
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  useSortable,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { IconArrowBackUp, IconArrowForwardUp, IconCheck, IconCode, IconCopy, IconDeviceDesktop, IconDeviceFloppy, IconDeviceMobile, IconEye, IconGripVertical, IconLetterT, IconPalette, IconPhoto, IconPlus, IconRectangle, IconSeparator, IconSpace, IconTrash } from '@tabler/icons-react';
// --- Types ---

type BlockType = 'text' | 'image' | 'button' | 'spacer' | 'divider' | 'header' | 'footer';

interface BaseBlock {
  id: string;
  type: BlockType;
}

interface TextBlock extends BaseBlock {
  type: 'text';
  content: string;
  align: 'left' | 'center' | 'right';
  fontSize: number;
  color: string;
}

interface ImageBlock extends BaseBlock {
  type: 'image';
  url: string;
  alt: string;
  link?: string;
  width: number | string;
  align: 'left' | 'center' | 'right';
}

interface ButtonBlock extends BaseBlock {
  type: 'button';
  text: string;
  url: string;
  color: string;
  textColor: string;
  align: 'left' | 'center' | 'right';
  borderRadius: number;
}

interface SpacerBlock extends BaseBlock {
  type: 'spacer';
  height: number;
}

interface DividerBlock extends BaseBlock {
  type: 'divider';
  color: string;
  thickness: number;
  padding: number;
}

interface HeaderBlock extends BaseBlock {
  type: 'header';
  title: string;
  logoUrl?: string;
  backgroundColor: string;
  textColor: string;
}

interface FooterBlock extends BaseBlock {
  type: 'footer';
  content: string;
  backgroundColor: string;
  textColor: string;
}

type Block = TextBlock | ImageBlock | ButtonBlock | SpacerBlock | DividerBlock | HeaderBlock | FooterBlock;

interface EmailSettings {
  backgroundColor: string;
  canvasColor: string;
  primaryColor: string;
  fontFamily: string;
  outlookCompatible: boolean;
}

interface EmailLayoutBuilderProps {
  opened: boolean;
  onClose: () => void;
  onApply: (html: string) => void;
  initialTemplate?: string;
  outlookCompatible?: boolean;
}

// --- Sortable Item Component ---

function SortableBlock({ 
  block, 
  isSelected, 
  onClick, 
  onDelete, 
  onDuplicate 
}: { 
  block: Block; 
  isSelected: boolean; 
  onClick: () => void; 
  onDelete: (id: string) => void;
  onDuplicate: (id: string) => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging
  } = useSortable({ id: block.id });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    position: 'relative' as const,
    zIndex: isDragging ? 100 : 1,
  };

  const renderPreview = () => {
    switch (block.type) {
      case 'header':
        return (
          <Box py="xl" px="md" bg={block.backgroundColor} style={{ textAlign: 'center' }}>
            {block.logoUrl && <img src={block.logoUrl} alt="Logo" style={{ maxHeight: 50, marginBottom: 10 }} />}
            <Text fw={700} size="xl" c={block.textColor}>{block.title}</Text>
          </Box>
        );
      case 'text':
        return (
          <Box py="md" px="md">
            <Text 
              style={{ textAlign: block.align, fontSize: block.fontSize, color: block.color, whiteSpace: 'pre-wrap' }}
            >
              {block.content || 'Click to edit text...'}
            </Text>
          </Box>
        );
      case 'image':
        return (
          <Box py="md" px="md" style={{ textAlign: block.align }}>
            {block.url ? (
              <img src={block.url} alt={block.alt} style={{ maxWidth: '100%', width: block.width }} />
            ) : (
              <Center bg="gray.1" h={100} style={{ border: '1px dashed #ccc' }}>
                <IconPhoto size="2rem" color="gray" />
              </Center>
            )}
          </Box>
        );
      case 'button':
        return (
          <Box py="md" px="md" style={{ textAlign: block.align }}>
            <Button 
              bg={block.color} 
              c={block.textColor} 
              style={{ borderRadius: block.borderRadius }}
            >
              {block.text}
            </Button>
          </Box>
        );
      case 'divider':
        return (
          <Box py={block.padding} px="md">
            <Divider color={block.color} size={block.thickness} />
          </Box>
        );
      case 'spacer':
        return <Box h={block.height} />;
      case 'footer':
        return (
          <Box py="lg" px="md" bg={block.backgroundColor} style={{ textAlign: 'center' }}>
            <Text size="xs" c={block.textColor}>{block.content}</Text>
          </Box>
        );
      default:
        return null;
    }
  };

  return (
    <Box 
      ref={setNodeRef} 
      style={style} 
      mb="xs"
      onPointerDown={(e) => {
          // Prevent drag when clicking buttons/inputs
          if ((e.target as HTMLElement).closest('button') || (e.target as HTMLElement).closest('input')) {
              return;
          }
          onClick();
      }}
    >
      <Paper 
        withBorder 
        p={0} 
        style={{ 
          cursor: 'pointer',
          borderColor: isSelected ? 'var(--mantine-color-blue-filled)' : undefined,
          boxShadow: isSelected ? '0 0 0 2px var(--mantine-color-blue-filled)' : undefined,
          overflow: 'hidden'
        }}
      >
        <Group gap={0} wrap="nowrap" align="stretch">
          <Center 
            {...attributes} 
            {...listeners} 
            px={4} 
            bg="gray.0" 
            style={{ cursor: 'grab', borderRight: '1px solid var(--mantine-color-gray-2)' }}
          >
            <IconGripVertical size="1rem" color="gray" />
          </Center>
          <Box style={{ flex: 1 }}>
            {renderPreview()}
          </Box>
          {isSelected && (
             <Stack gap={2} p={4} bg="blue.0" style={{ borderLeft: '1px solid var(--mantine-color-blue-2)' }}>
                <ActionIcon size="sm" variant="subtle" color="blue" onClick={(e) => { e.stopPropagation(); onDuplicate(block.id); }}>
                    <IconCopy size="0.8rem" />
                </ActionIcon>
                <ActionIcon size="sm" variant="subtle" color="red" onClick={(e) => { e.stopPropagation(); onDelete(block.id); }}>
                    <IconTrash size="0.8rem" />
                </ActionIcon>
             </Stack>
          )}
        </Group>
      </Paper>
    </Box>
  );
}

// --- Main Builder Component ---

export function EmailLayoutBuilder({ opened, onClose, onApply, outlookCompatible: initialOutlook }: EmailLayoutBuilderProps) {
  const [blocks, setBlocks] = useState<Block[]>([]);
  const [selectedBlockId, setSelectedBlockId] = useState<string | null>(null);
  const [settings, setSettings] = useState<EmailSettings>({
    backgroundColor: '#f4f4f4',
    canvasColor: '#ffffff',
    primaryColor: '#228be6',
    fontFamily: 'sans-serif',
    outlookCompatible: initialOutlook ?? true,
  });
  const [viewMode, setViewMode] = useState<'desktop' | 'mobile' | 'code' | 'preview'>('desktop');
  
  // History for undo/redo
  const [history, setHistory] = useState<Block[][]>([]);
  const [historyIndex, setHistoryIndex] = useState(-1);

  const addToHistory = useCallback((newBlocks: Block[]) => {
    const newHistory = history.slice(0, historyIndex + 1);
    newHistory.push([...newBlocks]);
    if (newHistory.length > 20) newHistory.shift();
    setHistory(newHistory);
    setHistoryIndex(newHistory.length - 1);
  }, [history, historyIndex]);

  // Initialize with a default layout if empty
  useEffect(() => {
    if (opened && blocks.length === 0) {
      const initialBlocks: Block[] = [
        { 
          id: 'h1', 
          type: 'header', 
          title: 'Welcome to Hermod', 
          backgroundColor: '#228be6', 
          textColor: '#ffffff' 
        } as HeaderBlock,
        { 
          id: 't1', 
          type: 'text', 
          content: 'Hello {{.name}},\n\nYour message from {{.table}} has been processed successfully.\n\nYou can customize this email by dragging and dropping blocks from the left panel.', 
          align: 'left', 
          fontSize: 16, 
          color: '#333333' 
        } as TextBlock,
        { 
          id: 'b1', 
          type: 'button', 
          text: 'View Details', 
          url: 'https://hermod.io/w/{{.id}}', 
          color: '#228be6', 
          textColor: '#ffffff', 
          align: 'center', 
          borderRadius: 4 
        } as ButtonBlock,
        { 
          id: 'f1', 
          type: 'footer', 
          content: '© 2024 Hermod Project. All rights reserved.', 
          backgroundColor: '#f9f9f9', 
          textColor: '#777777' 
        } as FooterBlock,
      ];
      setBlocks(initialBlocks);
      addToHistory(initialBlocks);
    }
  }, [opened, addToHistory, blocks.length]);

  const undo = () => {
    if (historyIndex > 0) {
      const prev = history[historyIndex - 1];
      setBlocks([...prev]);
      setHistoryIndex(historyIndex - 1);
    }
  };

  const redo = () => {
    if (historyIndex < history.length - 1) {
      const next = history[historyIndex + 1];
      setBlocks([...next]);
      setHistoryIndex(historyIndex + 1);
    }
  };

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;

    if (over && active.id !== over.id) {
      setBlocks((items) => {
        const oldIndex = items.findIndex((i) => i.id === active.id);
        const newIndex = items.findIndex((i) => i.id === over.id);
        const newBlocks = arrayMove(items, oldIndex, newIndex);
        addToHistory(newBlocks);
        return newBlocks;
      });
    }
  };

  const addBlock = (type: BlockType) => {
    const id = Math.random().toString(36).substr(2, 9);
    let newBlock: Block;

    switch (type) {
      case 'text':
        newBlock = { id, type, content: '', align: 'left', fontSize: 16, color: '#333333' } as TextBlock;
        break;
      case 'image':
        newBlock = { id, type, url: '', alt: 'Image', width: '100%', align: 'center' } as ImageBlock;
        break;
      case 'button':
        newBlock = { id, type, text: 'Button', url: '#', color: settings.primaryColor, textColor: '#ffffff', align: 'center', borderRadius: 4 } as ButtonBlock;
        break;
      case 'spacer':
        newBlock = { id, type, height: 20 } as SpacerBlock;
        break;
      case 'divider':
        newBlock = { id, type, color: '#eeeeee', thickness: 1, padding: 20 } as DividerBlock;
        break;
      case 'header':
        newBlock = { id, type, title: 'Header', backgroundColor: settings.primaryColor, textColor: '#ffffff' } as HeaderBlock;
        break;
      case 'footer':
        newBlock = { id, type, content: 'Footer text here', backgroundColor: '#f9f9f9', textColor: '#777777' } as FooterBlock;
        break;
      default: return;
    }

    const newBlocks = [...blocks, newBlock];
    setBlocks(newBlocks);
    setSelectedBlockId(id);
    addToHistory(newBlocks);
  };

  const deleteBlock = (id: string) => {
    const newBlocks = blocks.filter(b => b.id !== id);
    setBlocks(newBlocks);
    if (selectedBlockId === id) setSelectedBlockId(null);
    addToHistory(newBlocks);
  };

  const duplicateBlock = (id: string) => {
    const index = blocks.findIndex(b => b.id === id);
    if (index === -1) return;
    const newId = Math.random().toString(36).substr(2, 9);
    const newBlock = { ...blocks[index], id: newId };
    const newBlocks = [...blocks];
    newBlocks.splice(index + 1, 0, newBlock);
    setBlocks(newBlocks);
    setSelectedBlockId(newId);
    addToHistory(newBlocks);
  };

  const updateBlock = (id: string, updates: Partial<Block>) => {
    setBlocks(current => {
        const newBlocks = current.map(b => b.id === id ? { ...b, ...updates } as Block : b);
        // Optimization: only add to history if it's a significant change (debouncing would be better)
        // For now, we'll just update state. History is updated on drag end or block add/delete.
        return newBlocks;
    });
  };

  const selectedBlock = useMemo(() => blocks.find(b => b.id === selectedBlockId), [blocks, selectedBlockId]);

  const generateHtml = useCallback(() => {
    const { outlookCompatible, backgroundColor, canvasColor, fontFamily } = settings;
    
    const outlookHacks = outlookCompatible ? `
  <!--[if mso]>
  <style type="text/css">
    body, table, td, a { font-family: Arial, Helvetica, sans-serif !important; }
  </style>
  <![endif]-->` : '';

    const msoTableStart = outlookCompatible ? '<!--[if mso]><table role="presentation" width="600" align="center"><tr><td><![endif]-->' : '';
    const msoTableEnd = outlookCompatible ? '<!--[if mso]></td></tr></table><![endif]-->' : '';

    const renderBlockToHtml = (block: Block) => {
      switch (block.type) {
        case 'header':
          return `
          <tr>
            <td bgcolor="${block.backgroundColor}" style="padding: 30px; text-align: center;">
              ${block.logoUrl ? `<img src="${block.logoUrl}" alt="Logo" width="120" style="display: block; margin: 0 auto 15px;" />` : ''}
              <h1 style="color: ${block.textColor}; margin: 0; font-family: ${fontFamily}; font-size: 24px;">${block.title}</h1>
            </td>
          </tr>`;
        case 'text':
          return `
          <tr>
            <td style="padding: 20px 30px; color: ${block.color}; font-family: ${fontFamily}; line-height: 1.6; font-size: ${block.fontSize}px; text-align: ${block.align};">
              <div style="white-space: pre-wrap;">${block.content}</div>
            </td>
          </tr>`;
        case 'image':
          return `
          <tr>
            <td style="padding: 20px 30px; text-align: ${block.align};">
              ${block.link ? `<a href="${block.link}">` : ''}
              <img src="${block.url}" alt="${block.alt}" width="${block.width}" style="display: inline-block; max-width: 100%; height: auto;" />
              ${block.link ? `</a>` : ''}
            </td>
          </tr>`;
        case 'button':
          return `
          <tr>
            <td align="${block.align}" style="padding: 20px 30px;">
              <table border="0" cellspacing="0" cellpadding="0">
                <tr>
                  <td align="center" bgcolor="${block.color}" style="border-radius: ${block.borderRadius}px;">
                    <a href="${block.url}" target="_blank" style="padding: 12px 24px; color: ${block.textColor}; text-decoration: none; font-weight: bold; display: inline-block; font-family: ${fontFamily};">${block.text}</a>
                  </td>
                </tr>
              </table>
            </td>
          </tr>`;
        case 'divider':
          return `
          <tr>
            <td style="padding: ${block.padding}px 30px;">
              <hr style="border: none; border-top: ${block.thickness}px solid ${block.color}; margin: 0;" />
            </td>
          </tr>`;
        case 'spacer':
          return `<tr><td height="${block.height}" style="font-size: 1px; line-height: 1px;">&nbsp;</td></tr>`;
        case 'footer':
          return `
          <tr>
            <td style="padding: 20px 30px; background-color: ${block.backgroundColor}; text-align: center; color: ${block.textColor}; font-family: ${fontFamily}; font-size: 12px; border-top: 1px solid #eeeeee;">
              <p style="margin: 0;">${block.content}</p>
            </td>
          </tr>`;
        default: return '';
      }
    };

    const blocksHtml = blocks.map(renderBlockToHtml).join('\n');

    return `
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0"/>
  <style>
    body { margin: 0; padding: 0; min-width: 100%; font-family: ${fontFamily}; }
    img { height: auto; line-height: 100%; outline: none; text-decoration: none; border: 0; }
    table { border-collapse: collapse !important; }
    .content { width: 100%; max-width: 600px; }
    @media only screen and (max-width: 600px) {
      .content { width: 100% !important; }
    }
  </style>
  ${outlookHacks}
</head>
<body style="margin: 0; padding: 0; background-color: ${backgroundColor};">
  <table width="100%" border="0" cellspacing="0" cellpadding="0" bgcolor="${backgroundColor}">
    <tr>
      <td align="center" style="padding: 20px 0;">
        ${msoTableStart}
        <table width="600" border="0" cellspacing="0" cellpadding="0" bgcolor="${canvasColor}" class="content" style="border-radius: 8px; overflow: hidden; border: 1px solid #dddddd;">
          ${blocksHtml}
        </table>
        ${msoTableEnd}
      </td>
    </tr>
  </table>
</body>
</html>`.trim();
  }, [blocks, settings]);

  const handleApply = () => {
    onApply(generateHtml());
    onClose();
  };

  // --- UI Components ---

  const BlockEditor = () => {
    if (!selectedBlock) return <Center h="100%"><Text c="dimmed">Select a block to edit</Text></Center>;

    return (
      <Stack p="md">
        <Group justify="space-between">
          <Text fw={700} size="sm" style={{ textTransform: 'capitalize' }}>{selectedBlock.type} Properties</Text>
          <ActionIcon color="red" variant="subtle" onClick={() => deleteBlock(selectedBlock.id)}>
            <IconTrash size="1.2rem" />
          </ActionIcon>
        </Group>
        
        <Divider />

        {selectedBlock.type === 'header' && (
          <>
            <TextInput label="Title" value={selectedBlock.title} onChange={(e) => updateBlock(selectedBlock.id, { title: e.target.value })} />
            <TextInput label="Logo URL" value={selectedBlock.logoUrl || ''} onChange={(e) => updateBlock(selectedBlock.id, { logoUrl: e.target.value })} />
            <ColorInput label="Background" value={selectedBlock.backgroundColor} onChange={(val) => updateBlock(selectedBlock.id, { backgroundColor: val })} />
            <ColorInput label="Text Color" value={selectedBlock.textColor} onChange={(val) => updateBlock(selectedBlock.id, { textColor: val })} />
          </>
        )}

        {selectedBlock.type === 'text' && (
          <>
            <Textarea label="Content" minRows={5} value={selectedBlock.content} onChange={(e) => updateBlock(selectedBlock.id, { content: e.target.value })} />
            <Group grow>
                <Select 
                    label="Alignment" 
                    value={selectedBlock.align} 
                    onChange={(val) => updateBlock(selectedBlock.id, { align: val as any })}
                    data={[{value: 'left', label: 'Left'}, {value: 'center', label: 'Center'}, {value: 'right', label: 'Right'}]}
                />
                <NumberInput label="Font Size" value={selectedBlock.fontSize} onChange={(val) => updateBlock(selectedBlock.id, { fontSize: Number(val) })} />
            </Group>
            <ColorInput label="Color" value={selectedBlock.color} onChange={(val) => updateBlock(selectedBlock.id, { color: val })} />
          </>
        )}

        {selectedBlock.type === 'button' && (
          <>
            <TextInput label="Label" value={selectedBlock.text} onChange={(e) => updateBlock(selectedBlock.id, { text: e.target.value })} />
            <TextInput label="URL" value={selectedBlock.url} onChange={(e) => updateBlock(selectedBlock.id, { url: e.target.value })} />
            <Group grow>
                <ColorInput label="BG Color" value={selectedBlock.color} onChange={(val) => updateBlock(selectedBlock.id, { color: val })} />
                <ColorInput label="Text Color" value={selectedBlock.textColor} onChange={(val) => updateBlock(selectedBlock.id, { textColor: val })} />
            </Group>
            <Group grow>
                <Select 
                    label="Alignment" 
                    value={selectedBlock.align} 
                    onChange={(val) => updateBlock(selectedBlock.id, { align: val as any })}
                    data={[{value: 'left', label: 'Left'}, {value: 'center', label: 'Center'}, {value: 'right', label: 'Right'}]}
                />
                <NumberInput label="Radius" value={selectedBlock.borderRadius} onChange={(val) => updateBlock(selectedBlock.id, { borderRadius: Number(val) })} />
            </Group>
          </>
        )}

        {selectedBlock.type === 'image' && (
          <>
            <TextInput label="Image URL" value={selectedBlock.url} onChange={(e) => updateBlock(selectedBlock.id, { url: e.target.value })} />
            <TextInput label="Alt Text" value={selectedBlock.alt} onChange={(e) => updateBlock(selectedBlock.id, { alt: e.target.value })} />
            <TextInput label="Link URL" value={selectedBlock.link || ''} onChange={(e) => updateBlock(selectedBlock.id, { link: e.target.value })} />
            <Group grow>
                <TextInput label="Width (px or %)" value={selectedBlock.width} onChange={(e) => updateBlock(selectedBlock.id, { width: e.target.value })} />
                <Select 
                    label="Alignment" 
                    value={selectedBlock.align} 
                    onChange={(val) => updateBlock(selectedBlock.id, { align: val as any })}
                    data={[{value: 'left', label: 'Left'}, {value: 'center', label: 'Center'}, {value: 'right', label: 'Right'}]}
                />
            </Group>
          </>
        )}

        {selectedBlock.type === 'spacer' && (
          <NumberInput label="Height (px)" value={selectedBlock.height} onChange={(val) => updateBlock(selectedBlock.id, { height: Number(val) })} />
        )}

        {selectedBlock.type === 'divider' && (
          <>
            <ColorInput label="Color" value={selectedBlock.color} onChange={(val) => updateBlock(selectedBlock.id, { color: val })} />
            <NumberInput label="Thickness" value={selectedBlock.thickness} onChange={(val) => updateBlock(selectedBlock.id, { thickness: Number(val) })} />
            <NumberInput label="Padding Y" value={selectedBlock.padding} onChange={(val) => updateBlock(selectedBlock.id, { padding: Number(val) })} />
          </>
        )}

        {selectedBlock.type === 'footer' && (
          <>
            <Textarea label="Content" value={selectedBlock.content} onChange={(e) => updateBlock(selectedBlock.id, { content: e.target.value })} />
            <ColorInput label="Background" value={selectedBlock.backgroundColor} onChange={(val) => updateBlock(selectedBlock.id, { backgroundColor: val })} />
            <ColorInput label="Text Color" value={selectedBlock.textColor} onChange={(val) => updateBlock(selectedBlock.id, { textColor: val })} />
          </>
        )}
      </Stack>
    );
  };

  const Sidebar = () => (
    <Stack p="md" gap="xs">
      <Text fw={700} size="xs" c="dimmed" mb={4}>DRAG OR CLICK TO ADD</Text>
      
      <SimpleGrid cols={2} spacing="xs">
        <UnstyledButton onClick={() => addBlock('header')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconRectangle size="1.5rem" />
          <Text size="xs">Header</Text>
        </UnstyledButton>
        <UnstyledButton onClick={() => addBlock('text')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconLetterT size="1.5rem" />
          <Text size="xs">Text</Text>
        </UnstyledButton>
        <UnstyledButton onClick={() => addBlock('image')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconPhoto size="1.5rem" />
          <Text size="xs">Image</Text>
        </UnstyledButton>
        <UnstyledButton onClick={() => addBlock('button')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconRectangle size="1.5rem" />
          <Text size="xs">Button</Text>
        </UnstyledButton>
        <UnstyledButton onClick={() => addBlock('divider')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconSeparator size="1.5rem" />
          <Text size="xs">Divider</Text>
        </UnstyledButton>
        <UnstyledButton onClick={() => addBlock('spacer')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconSpace size="1.5rem" />
          <Text size="xs">Spacer</Text>
        </UnstyledButton>
        <UnstyledButton onClick={() => addBlock('footer')} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8, textAlign: 'center' }}>
          <IconRectangle size="1.5rem" />
          <Text size="xs">Footer</Text>
        </UnstyledButton>
      </SimpleGrid>

      <Divider my="md" label="Global Styles" labelPosition="center" />
      
      <Stack gap="xs">
        <ColorInput size="xs" label="Background Color" value={settings.backgroundColor} onChange={(val) => setSettings({...settings, backgroundColor: val})} />
        <ColorInput size="xs" label="Canvas Color" value={settings.canvasColor} onChange={(val) => setSettings({...settings, canvasColor: val})} />
        <Select 
            size="xs" 
            label="Font Family" 
            value={settings.fontFamily} 
            onChange={(val) => setSettings({...settings, fontFamily: val || 'sans-serif'})}
            data={[
                { value: 'sans-serif', label: 'Sans Serif' },
                { value: 'serif', label: 'Serif' },
                { value: 'monospace', label: 'Monospace' },
                { value: 'Arial, sans-serif', label: 'Arial' },
                { value: '"Times New Roman", serif', label: 'Times New Roman' },
            ]}
        />
        <UnstyledButton onClick={() => setSettings({...settings, outlookCompatible: !settings.outlookCompatible})} p="xs" style={{ border: '1px solid var(--mantine-color-gray-2)', borderRadius: 8 }}>
            <Group justify="space-between">
                <Text size="xs">Outlook Optimization</Text>
                {settings.outlookCompatible ? <IconCheck size="1rem" color="green" /> : <Box style={{width:16}}/>}
            </Group>
        </UnstyledButton>
      </Stack>
    </Stack>
  );

  return (
    <Modal 
      opened={opened} 
      onClose={onClose} 
      fullScreen 
      withCloseButton={false}
      padding={0}
      styles={{
        content: { display: 'flex', flexDirection: 'column' },
        body: { flex: 1, display: 'flex', flexDirection: 'column', height: '100%' }
      }}
    >
      <AppShell
        header={{ height: 60 }}
        navbar={{ width: 280, breakpoint: 'sm' }}
        aside={{ width: 320, breakpoint: 'md' }}
        padding="md"
      >
        <AppShell.Header>
          <Group h="100%" px="md" justify="space-between">
            <Group>
                <IconPalette color="var(--mantine-color-blue-filled)" />
                <Text fw={700}>Hermod Email Designer</Text>
                <Divider orientation="vertical" />
                <Group gap={5}>
                    <Tooltip label="Undo">
                        <ActionIcon variant="subtle" disabled={historyIndex <= 0} onClick={undo}><IconArrowBackUp size="1.2rem" /></ActionIcon>
                    </Tooltip>
                    <Tooltip label="Redo">
                        <ActionIcon variant="subtle" disabled={historyIndex >= history.length - 1} onClick={redo}><IconArrowForwardUp size="1.2rem" /></ActionIcon>
                    </Tooltip>
                    <Divider orientation="vertical" />
                    <Button variant="subtle" color="red" size="compact-xs" leftSection={<IconTrash size="0.8rem" />} onClick={() => { if(confirm('Clear all blocks?')) { setBlocks([]); addToHistory([]); } }}>
                        Clear Canvas
                    </Button>
                </Group>
            </Group>

            <Group>
                <Box visibleFrom="sm">
                    <Group gap="xs">
                        <Tooltip label="Desktop View">
                            <ActionIcon variant={viewMode === 'desktop' ? 'filled' : 'subtle'} onClick={() => setViewMode('desktop')}><IconDeviceDesktop size="1.2rem" /></ActionIcon>
                        </Tooltip>
                        <Tooltip label="Mobile View">
                            <ActionIcon variant={viewMode === 'mobile' ? 'filled' : 'subtle'} onClick={() => setViewMode('mobile')}><IconDeviceMobile size="1.2rem" /></ActionIcon>
                        </Tooltip>
                        <Tooltip label="Live Preview">
                            <ActionIcon variant={viewMode === 'preview' ? 'filled' : 'subtle'} onClick={() => setViewMode('preview')}><IconEye size="1.2rem" /></ActionIcon>
                        </Tooltip>
                        <Tooltip label="View Code">
                            <ActionIcon variant={viewMode === 'code' ? 'filled' : 'subtle'} onClick={() => setViewMode('code')}><IconCode size="1.2rem" /></ActionIcon>
                        </Tooltip>
                    </Group>
                </Box>
                <Divider orientation="vertical" />
                <Button variant="outline" onClick={onClose}>Discard</Button>
                <Button leftSection={<IconDeviceFloppy size="1rem" />} onClick={handleApply}>Save Template</Button>
            </Group>
          </Group>
        </AppShell.Header>

        <AppShell.Navbar p={0}>
            <ScrollArea h="100%">
                <Sidebar />
            </ScrollArea>
        </AppShell.Navbar>

        <AppShell.Main bg="gray.1" style={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
            <ScrollArea style={{ flex: 1 }}>
                <Center py="xl">
                    {viewMode === 'code' ? (
                        <Paper withBorder p="md" shadow="sm" style={{ width: '100%', maxWidth: 1000 }}>
                            <Code block style={{ fontSize: rem(12) }}>{generateHtml()}</Code>
                        </Paper>
                    ) : viewMode === 'preview' ? (
                        <Paper withBorder shadow="md" style={{ width: '100%', maxWidth: 800, height: 800, overflow: 'hidden' }}>
                             <iframe 
                                title="Preview"
                                srcDoc={generateHtml().replace(/{{/g, '&#123;&#123;').replace(/}}/g, '&#125;&#125;')} 
                                style={{ width: '100%', height: '100%', border: 'none' }}
                            />
                        </Paper>
                    ) : (
                        <Box 
                            style={{ 
                                width: viewMode === 'mobile' ? 375 : 600, 
                                transition: 'width 0.3s ease',
                                boxShadow: '0 10px 30px rgba(0,0,0,0.1)',
                                minHeight: 600,
                                backgroundColor: settings.canvasColor,
                                borderRadius: 8,
                                overflow: 'hidden'
                            }}
                            onClick={() => setSelectedBlockId(null)}
                        >
                            <DndContext 
                                sensors={sensors}
                                collisionDetection={closestCenter}
                                onDragEnd={handleDragEnd}
                            >
                                <SortableContext 
                                    items={blocks.map(b => b.id)}
                                    strategy={verticalListSortingStrategy}
                                >
                                    {blocks.map((block) => (
                                        <SortableBlock 
                                            key={block.id} 
                                            block={block} 
                                            isSelected={selectedBlockId === block.id}
                                            onClick={() => setSelectedBlockId(block.id)}
                                            onDelete={deleteBlock}
                                            onDuplicate={duplicateBlock}
                                        />
                                    ))}
                                </SortableContext>
                            </DndContext>
                            {blocks.length === 0 && (
                                <Center h={400}>
                                    <Stack align="center" gap="xs">
                                        <IconPlus size="3rem" color="gray" />
                                        <Text c="dimmed">Add your first block from the sidebar</Text>
                                    </Stack>
                                </Center>
                            )}
                        </Box>
                    )}
                </Center>
            </ScrollArea>
        </AppShell.Main>

        <AppShell.Aside p={0}>
             <ScrollArea h="100%">
                <BlockEditor />
            </ScrollArea>
        </AppShell.Aside>
      </AppShell>
    </Modal>
  );
}


