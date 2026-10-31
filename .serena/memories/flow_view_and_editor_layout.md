# A flow's view and editor: one layout, one live store

The workflow pages after the redesign (#335, #337, #338, #340, #341). Read this
before changing anything under `ui/src/pages/workflows/`.

## One frame for both pages (#337)

The read-only view (`WorkflowDetailPage.tsx`, canvas `DetailFlowCanvas.tsx`) and
the editor (`WorkflowEditorPage.tsx`) share: their own gutter,
`components/flows/FlowHeader.tsx` (back, name with one state line, actions, ⋯),
the tabs (`flowTabs.ts`) and a canvas card.

- `components/flows/CanvasControls.tsx` is the one bottom-left bar for zoom, Fit
  and Tidy up (replaced React Flow's Controls and the header icons).
- Every step is the same card on both pages. A source/sink without a label is
  called by its connection's name, not its id. Branch outputs (Condition
  TRUE/FALSE, Approval, Switch, Router, AI Classify, Foreach, Deduplicate) are
  rows at the card's foot with the handle level with its name (`BranchOutputs`).
- Tidy up passes each step's *measured* size to dagre
  (`ui/src/workers/layoutPositions.ts`), so tall cards do not overlap.
- Phone: actions move to a bottom bar in place of the app's tabs, tabs scroll,
  the view draws the flow top to bottom.

## Height (#335)

The redesign removed the AppShell header, so the old "viewport minus header,
fallback 60px" left a blank strip. The editor sizes from the header offset (0
without a header) and on a phone subtracts `BOTTOM_TABS_HEIGHT`
(`components/layout/nav.ts`), which the tabs, their spacer and the editor share.

## Live data in the read-only view (#341)

The view used to keep only running/stopped from the status frame. It now fills the
editor's live store from the same feed (`useWorkflowWebSockets`):
- `LiveNote` in the canvas corner: Live / Live data paused · Reconnecting… /
  Stopped.
- Card counts, errors, problems and the latest record behind the eye come from
  `WorkflowEditor/nodes/stepLive.tsx`, shared with `BaseNode` — change it once.
- Lines are `LiveEdge` (not deletable), moving only while messages cross them.
- `SampleInspector.tsx` is shared by both pages.
- The phone layout uses each card's measured height.

## Node settings width (#338)

The drawer width is a `clamp()` with a floor (a phone gets the whole width), and
rows break on the width they actually have: `components/common/FormRow.tsx` and
the transformation layout grid use **container queries**. Do not add
`<Group grow>` rows or media-query grids to node settings —
`formRow.test.tsx` fails on them; `node_form_space_e2e.spec.ts` measures field
widths on a phone and a laptop.

## Traces tab (#340)

Mantine `Grid h="100%"` wraps columns in an element with no height, so nothing
scrolled. The panes are now a CSS grid bounded by the tab, each scrolling on its
own; on a phone it is one pane at a time (list, then journey with "All traces").
A step is `traces/JourneyStep.tsx`; on a phone input/output sit behind a switch.
