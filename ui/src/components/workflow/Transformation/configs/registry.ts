import type { ComponentType } from 'react'

// data
import { MappingConfig } from './data/MappingConfig'
import { FilterConfig } from './data/FilterConfig'
import { SetFieldsConfig } from './data/SetFieldsConfig'
import { AggregateConfig } from './data/AggregateConfig'
import { MaskConfig } from './data/MaskConfig'
import { AdvancedConfig } from './data/AdvancedConfig'
import { PipelineConfig } from './data/PipelineConfig'
import { ValidatorConfig } from './data/ValidatorConfig'
import { CharMapConfig } from './data/CharMapConfig'
import { DataConversionConfig } from './data/DataConversionConfig'
import { SamplingConfig } from './data/SamplingConfig'
import { UnpivotConfig } from './data/UnpivotConfig'
import { PivotConfig } from './data/PivotConfig'
import { SCDConfig } from './data/SCDConfig'
import { FilterDataConfig } from './data/FilterDataConfig'

// enrichment
import { LookupConfig } from './enrichment/LookupConfig'
import { DBLookupConfig } from './enrichment/DBLookupConfig'
import { SQLConfig } from './enrichment/SQLConfig'
import { FuzzyLookupConfig } from './enrichment/FuzzyLookupConfig'
import { TermExtractionConfig } from './enrichment/TermExtractionConfig'
import { APILookupConfig } from './enrichment/APILookupConfig'
import { AIConfig } from './enrichment/AIConfig'

// logic
import { ConditionConfig } from './logic/ConditionConfig'
import { SwitchConfig } from './logic/SwitchConfig'
import { RouterConfig } from './logic/RouterConfig'
import { WaitConfig } from './logic/WaitConfig'
import { JoinConfig } from './logic/JoinConfig'
import { ForeachConfig } from './logic/ForeachConfig'
import { CollectConfig } from './logic/CollectConfig'
import { CircuitBreakerConfig } from './logic/CircuitBreakerConfig'
import { ApprovalConfig } from './logic/ApprovalConfig'
import { StatefulConfig } from './logic/StatefulConfig'
import { LogConfig } from './logic/LogConfig'
import { MulticastConfig } from './logic/MulticastConfig'
import { RateLimitConfig } from './logic/RateLimitConfig'
import { JoinFieldsConfig } from './logic/JoinFieldsConfig'

// script
import { LuaConfig } from './script/LuaConfig'
import { WasmConfig } from './script/WasmConfig'

// util / quality
import { DeduplicateConfig } from './util/DeduplicateConfig'
import { StatValidatorConfig } from './quality/StatValidatorConfig'
import { RowCountConfig } from './quality/RowCountConfig'
import { AuditConfig } from './quality/AuditConfig'
import { DQScorerConfig } from './quality/DQScorerConfig'

/**
 * Every prop a config component may receive. Components declare only the ones
 * they use; the dispatcher always passes the whole set.
 */
export interface TransformConfigProps {
  config: any
  updateNodeConfig: (id: string, config: any) => void
  nodeId: string
  availableFields?: any[]
  fieldPaths?: string[]
  sources?: any[]
  incomingPayload?: any
  onTest?: () => void
  testing?: boolean
  addField?: (...args: any[]) => void
  onAddFromSource?: (...args: any[]) => void
  testLookup?: () => void
}

// Components accept a subset of TransformConfigProps, so the registry stores
// them as accepting `any`. The dispatcher always supplies the full set, and
// each component's own prop interface remains the checked contract at its
// definition site.
type ConfigComponent = ComponentType<any>

/**
 * Maps a transformation subtype to the component that configures it.
 *
 * This replaced a 593-line `switch` preceded by a chain of
 * `if (node.type === …)` checks — two dispatch mechanisms for one decision.
 * With a registry, adding a transformation means adding a file and one entry
 * here, and never editing the form itself.
 *
 * Keys are `transType` values (see nodeCategories.ts).
 */
export const TRANSFORM_CONFIGS: Record<string, ConfigComponent> = {
  // data shaping
  mapping: MappingConfig,
  filter: FilterConfig,
  filter_data: FilterDataConfig,
  set: SetFieldsConfig,
  aggregate: AggregateConfig,
  mask: MaskConfig,
  pii_masking: MaskConfig,
  mask_emails: MaskConfig,
  advanced: AdvancedConfig,
  pipeline: PipelineConfig,
  validator: ValidatorConfig,
  validate: ValidatorConfig,
  char_map: CharMapConfig,
  data_conversion: DataConversionConfig,
  sampling: SamplingConfig,
  unpivot: UnpivotConfig,
  pivot: PivotConfig,
  scd: SCDConfig,

  // enrichment
  lookup: LookupConfig,
  db_lookup: DBLookupConfig,
  execute_sql: SQLConfig,
  fuzzy_lookup: FuzzyLookupConfig,
  term_extraction: TermExtractionConfig,
  api_lookup: APILookupConfig,
  ai_enrichment: AIConfig,
  ai_mapper: AIConfig,

  // logic & flow
  condition: ConditionConfig,
  switch: SwitchConfig,
  router: RouterConfig,
  wait: WaitConfig,
  join: JoinFieldsConfig,
  foreach: ForeachConfig,
  fanout: ForeachConfig,
  collect: CollectConfig,
  circuit_breaker: CircuitBreakerConfig,
  approval: ApprovalConfig,
  stateful: StatefulConfig,
  log: LogConfig,
  multicast: MulticastConfig,
  rate_limit: RateLimitConfig,

  // scripting
  lua: LuaConfig,
  wasm: WasmConfig,

  // quality & utility
  deduplicate: DeduplicateConfig,
  stat_validator: StatValidatorConfig,
  row_count: RowCountConfig,
  audit: AuditConfig,
  audit_fields: AuditConfig,
  dq_scorer: DQScorerConfig,
}

/**
 * Node types that carry their own editor regardless of transType — a `wait`
 * node is always configured by WaitConfig, whatever subtype it claims.
 */
export const NODE_TYPE_CONFIGS: Record<string, ConfigComponent> = {
  wait: WaitConfig,
  join: JoinConfig,
  circuit_breaker: CircuitBreakerConfig,
  foreach: ForeachConfig,
  collect: CollectConfig,
  log: LogConfig,
  deduplicate: DeduplicateConfig,
  approval: ApprovalConfig,
  stateful: StatefulConfig,
  condition: ConditionConfig,
  switch: SwitchConfig,
  router: RouterConfig,
  multicast: MulticastConfig,
  validator: ValidatorConfig,
}

/**
 * Resolves the editor for a node. Node type wins over transType, matching the
 * previous dispatcher's ordering.
 */
export function resolveConfigComponent(
  nodeType?: string,
  transType?: string,
): ConfigComponent | undefined {
  if (nodeType && NODE_TYPE_CONFIGS[nodeType]) return NODE_TYPE_CONFIGS[nodeType]
  if (transType && TRANSFORM_CONFIGS[transType]) return TRANSFORM_CONFIGS[transType]
  return undefined
}
