import { NODE_TYPE_CONFIGS } from '@/components/workflow/Transformation/configs/registry'

/**
 * Node types whose editor lives inside TransformationForm without a registry
 * entry: `merge` gets an explanatory panel there, and `note` predates the
 * registry. Everything else is discovered from NODE_TYPE_CONFIGS.
 */
const EXTRA_EDITOR_TYPES = ['merge', 'note'] as const

/**
 * Whether WorkflowNodeSettingsModal should render TransformationForm for a node.
 *
 * This used to be a literal list in the modal, and it had drifted from the
 * registry: nine node types with a registered editor — foreach, collect, wait,
 * join, circuit_breaker, approval, log, deduplicate, multicast — opened a panel
 * with no editor in it, so a Foreach (Fan-out) node could never be given its
 * arrayPath. Reading the registry means registering an editor is all it takes
 * for a node type to become configurable.
 *
 * `transformation` and `validator` are here because the modal serves them too;
 * `source` and `sink` have their own forms and must not be claimed.
 */
export function rendersNodeEditor(nodeType?: string): boolean {
  if (!nodeType) return false
  if (nodeType === 'source' || nodeType === 'sink') return false
  if (nodeType === 'transformation' || nodeType === 'validator') return true
  if (EXTRA_EDITOR_TYPES.includes(nodeType as (typeof EXTRA_EDITOR_TYPES)[number])) return true
  return nodeType in NODE_TYPE_CONFIGS
}
