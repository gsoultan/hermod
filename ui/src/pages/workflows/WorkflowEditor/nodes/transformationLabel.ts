/** What a transformation step is called on the canvas, by its transType. */
export function transformationLabel(transType: string | undefined): string {
  switch (transType) {
    case 'mapping': return 'Mapping';
    case 'set': return 'Set Fields';
    case 'filter_data': return 'Filter';
    case 'mask': return 'Mask';
    case 'encrypt': return 'Encrypt';
    case 'decrypt': return 'Decrypt';
    case 'db_lookup': return 'DB Lookup';
    case 'api_lookup': return 'API Lookup';
    case 'redis_lookup': return 'Redis Lookup';
    case 'redis_set': return 'Redis Set';
    case 'panmail_providers': return 'Panmail Providers';
    case 'fuzzy_lookup': return 'Fuzzy Lookup';
    case 'char_map': return 'Char Map';
    case 'data_conversion': return 'Data Conversion';
    case 'execute_sql': return 'Execute SQL';
    case 'parallel_pipeline': return 'Parallel Pipeline';
    case 'pipeline': return 'Pipeline';
    case 'advanced': return 'Advanced';
    case 'lua': return 'Lua Script';
    case 'aggregate': return 'Aggregate';
    case 'sampling': return 'Sampling';
    case 'foreach': return 'Foreach';
    case 'fanout': return 'Fanout';
    case 'validator': return 'Validator';
    case 'stat_validator': return 'Statistical Validation';
    case 'dq_scorer': return 'Data Quality Scorer';
    case 'ai_prompt': return 'AI Prompt';
    case 'ai_extract': return 'AI Extract';
    case 'ai_embed': return 'AI Embed';
    case 'ai_retrieve': return 'AI Retrieve';
    case 'flatten': return 'Flatten';
    case 'unflatten': return 'Unflatten';
    case 'parse_field': return 'Parse Field';
    case 'template_render': return 'Template';
    case 'field_diff': return 'Field Diff';
    case 'reference_lookup': return 'Reference Lookup';
    case 'geo': return 'Geo';
    case 'scale': return 'Scale';
    case 'encode': return 'Encode';
    case 'bucketize': return 'Bucketize';
    case 'rolling': return 'Rolling Features';
    case 'anomaly_score': return 'Anomaly Score';
    default: return 'Transformation';
  }
}
