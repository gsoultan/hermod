import { Button, Tooltip } from '@mantine/core';
import { IconFocus2, IconLayoutDistributeHorizontal, IconMinus, IconPlus } from '@tabler/icons-react';

interface CanvasControlsProps {
  zoom: number;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  /** Left out where the layout is not the reader's to change. */
  onTidy?: () => void;
}

/**
 * The one control bar on a flow canvas, in its bottom-left corner: zoom, Fit
 * and, in the editor, Tidy up. It replaces React Flow's default buttons and
 * the icons that used to crowd the page header.
 */
export function CanvasControls({ zoom, onZoomIn, onZoomOut, onFit, onTidy }: CanvasControlsProps) {
  return (
    <Button.Group aria-label="Canvas controls" className="hermod-canvas-controls">
      <Tooltip label="Zoom out" withArrow>
        <Button aria-label="Zoom out" variant="default" px={10} onClick={onZoomOut}>
          <IconMinus size="1rem" />
        </Button>
      </Tooltip>
      <Button.GroupSection variant="default" fz="xs" fw={600} c="dimmed" miw={52} px={0} visibleFrom="sm" style={{ fontVariantNumeric: 'tabular-nums' }}>
        {Math.round(zoom * 100)}%
      </Button.GroupSection>
      <Tooltip label="Zoom in" withArrow>
        <Button aria-label="Zoom in" variant="default" px={10} onClick={onZoomIn}>
          <IconPlus size="1rem" />
        </Button>
      </Tooltip>
      <Button aria-label="Fit to view" variant="default" px={12} leftSection={<IconFocus2 size="0.9rem" />} onClick={onFit}>
        Fit
      </Button>
      {onTidy && (
        <Button aria-label="Tidy up" variant="default" px={12} leftSection={<IconLayoutDistributeHorizontal size="0.9rem" />} onClick={onTidy}>
          <span className="hermod-hide-phone">Tidy up</span>
        </Button>
      )}
    </Button.Group>
  );
}
