package builder

// descriptions tells the model what each node kind does. Keys are Kind.Key.
// TestEveryCatalogueKindIsDescribed fails when a registered node has no entry
// or an entry names a node that is not registered, so this cannot drift from
// what the binary can run.
var descriptions = map[string]string{
	// Structural.
	"source": "Entry point: where records come from (webhook, chat, database CDC, queue, schedule, file...). " +
		"Set ref_id to the id of one of the available sources listed below, or leave it empty and say in " +
		`config_json which kind is needed, e.g. {"sourceType":"webhook"}. A chat source ({"sourceType":"chat"}) ` +
		`receives {conversation_id, message, user, metadata} from a web widget, Slack or Telegram and answers ` +
		`with the record's ai_output field: put an ai_prompt with memory after it to build a chatbot.`,
	"sink": "Destination a record is written to (database, queue, API, notification...). Set ref_id to " +
		"the id of one of the available sinks listed below, or leave it empty and say in config_json " +
		`which kind is needed, e.g. {"sinkType":"slack"}. {"sinkType":"ml_dataset"} is Collect Dataset: it appends ` +
		`each record as a row to a dataset of the workflow's vhost on the ML worker, for Train Model or a retrain ` +
		`policy to learn from; its sink config is {"dataset","column_mappings","mask_fields","mask_type","max_rows"}.`,

	// Node executors.
	"ai_agent": `Language-model agent that pursues a goal with a bounded loop over the tools it is given. config: ` +
		`{"provider","model","apiKey":"{{secret(\"NAME\")}}","goal","system","maxSteps","tools":[{"name","description",` +
		`"kind":"db_lookup|api_lookup|ai_retrieve|sink|mcp","config":{...},"nodeId":"<sink node id, for kind sink>",` +
		`"server":{"url":"https://...","headers":{"Authorization":"Bearer {{secret(\"NAME\")}}"}},"tool":"<remote tool, for kind mcp>",` +
		`"write","requireApproval","parameters":[{"name","type","description","required"}]}]}. A kind mcp tool calls ` +
		`that one tool of a remote MCP server (parameters default to its input schema). Sink tools, tools marked write, ` +
		`and mcp tools pause for human approval unless requireApproval is false; an mcp tool runs unapproved only when ` +
		`it sets "write": false and its server also marks it read-only. ` +
		`Writes the answer to targetField (default "ai_agent_answer").`,
	"ai_classify": `Language-model classifier and router. config: {"provider","model","apiKey":"{{secret(\"NAME\")}}",` +
		`"labels":"billing,bug,refund","instructions","threshold","targetField"}. Each label is an outgoing ` +
		`branch: use the label as the edge's source_handle, plus "unsure" below the threshold.`,
	"approval":        `Human approval gate: pauses the record until someone approves or rejects it. Branches: "approved", "rejected".`,
	"circuit_breaker": "Stops the flow after repeated downstream failures and resumes after a cool-down.",
	"collect":         "Fan-in: waits for every item of an earlier fan-out and continues once all have arrived.",
	"condition": `If/else. config: {"field":"amount","operator":"gt","value":"100"} or {"conditions":[...]}. ` +
		`Branches: "true" and "false" (edge source_handle).`,
	"deduplicate": "Drops records already seen, by a key, within a time window.",
	"explode": `Splits a record with an array into one record per element, keeping its other fields and dropping ` +
		`the array. config: {"arrayPath":"lines","mode":"field|merge","targetField":"line","indexField","maxItems":"10000",` +
		`"keepEmpty"}. mode field puts the element in targetField (default the array's path); merge copies an object ` +
		`element's fields onto the record.`,
	"foreach":  `Splits an array into one record per item; downstream nodes run for each. config: {"arrayPath":"items"}.`,
	"join":     "Stateful join: waits for related events from several paths and joins them by key.",
	"log":      "Writes the record to the workflow log; passes it on unchanged.",
	"router":   "Content router: sends the record down the branch whose pattern rule it matches.",
	"stateful": "Stores and recalls workflow state (counters, last values) between records.",
	"switch": `Multi-way branch on one field. config: {"field":"status","cases":[{"value":"open"},{"value":"closed"}]}. ` +
		`Branch names are the case values, plus "default".`,
	"validator": "Validates required fields and formats; invalid records fail the node.",
	"wait":      "Pauses the record for a duration (long waits are suspended and resumed).",

	// Transformers (node type "transformation", config.transType = the name).
	"transformation:advanced":  "Power-user field transforms with expressions.",
	"transformation:aggregate": "Groups records and computes sums, counts and averages over a window.",
	"transformation:ai_embed": `Computes an embedding vector of text for a vector store. config: {"provider","model",` +
		`"apiKey":"{{secret(\"NAME\")}}","targetField"}.`,
	"transformation:ai_enrichment": "Legacy AI enrichment; prefer transformation:ai_prompt.",
	"transformation:ai_extract": `Extracts structured fields with a language model against a JSON Schema. config: {"provider",` +
		`"model","apiKey":"{{secret(\"NAME\")}}","schema":"<JSON Schema>","instructions","targetField"}.`,
	"transformation:ai_mapper": "Legacy AI mapper; prefer transformation:ai_extract.",
	"transformation:ai_retrieve": `Retrieval for RAG: embeds a query and returns the closest documents from a vector ` +
		`store. config: {"provider","model","apiKey":"{{secret(\"NAME\")}}","store":"pgvector|pinecone","query" or ` +
		`"queryField","topK","minScore","targetField"}; pgvector also needs "connectionString" and "table", pinecone "indexHost" ` +
		`and "storeApiKey", each as a {{secret(\"NAME\")}} reference.`,
	"transformation:ml_predict": `Calls a machine-learning model registered in the workflow's vhost and writes its ` +
		`prediction onto the record. config: {"model":"<registered model name>","inputs":{"feature":"field.path"},` +
		`"outputField":"prediction"}. Empty inputs send the whole record.`,
	"transformation:ml_train": `Trains a new version of a model on a dataset held by the vhost's ML worker and writes ` +
		`the result (version, metrics, whether it went live) onto the record. config: {"model","dataset","target",` +
		`"features":"a,b,c","task":"auto|classification|regression",` +
		`"algorithm":"auto|random_forest|gradient_boosting|linear|xgboost|pytorch_mlp|keras_mlp",` +
		`"hiddenLayers":"64,32","epochs","batchSize","learningRate","patience",` +
		`"goLive":"never|always|metric","goLiveMetric","goLiveMin","goLiveMax","sourceId","query","maxRows",` +
		`"outputField":"training"}. A sourceId and read-only query refill the dataset first. pytorch_mlp and ` +
		`keras_mlp need the worker's -dl image; hiddenLayers..patience tune only them.`,
	"transformation:scale": `Rescales numeric fields with statistics fitted beforehand (never refitted). config: ` +
		`{"method":"minmax|zscore","fields":[{"field":"amount","min":0,"max":500,"targetField":"amount_scaled"}],` +
		`"stats":{"amount":{"mean":120,"std":40}},"clip":false,"onMissing":"fail|skip"}. A row without numbers ` +
		`takes them from stats; with no rows every field in stats is scaled.`,
	"transformation:encode": `Encodes a categorical field. config: {"field","method":"onehot|label|hash"}; onehot: ` +
		`{"categories":["ID","SG"],"prefix":"country_","otherBucket":true} writes one 0/1 field per category plus ` +
		`<prefix>other; label: {"mapping":{"S":0,"M":1},"unknownValue":-1,"targetField"}; hash: {"buckets":16,` +
		`"targetField"} (FNV-1a).`,
	"transformation:bucketize": `Puts a numeric field into bins. config: {"field","edges":[0,18,65,120],` +
		`"labels":["child","adult","senior"],"outOfRange":"null|clip|fail","targetField"}. Bins hold their lower edge.`,
	"transformation:rolling": `Per-key rolling features over the last N events or a time window. config: {"field",` +
		`"keyBy":"customer_id","windowType":"count|time","size":20,"window":"10m","features":["count","sum","mean",` +
		`"std","min","max"],"prefix":"amount_","maxKeys":10000,"persistent":false}. Includes the current record.`,
	"transformation:anomaly_score": `Scores a field against its per-key rolling history and flags outliers. config: ` +
		`{"field","keyBy","method":"zscore|iqr","threshold":3,"windowType":"count|time","size":50,"window":"1h",` +
		`"minEvents":10,"scoreField","flagField","persistent":false}. Writes <field>_anomaly_score and <field>_is_anomaly.`,
	"transformation:ai_prompt": `Generates text or JSON with a language model. config: {"provider","model",` +
		`"apiKey":"{{secret(\"NAME\")}}","prompt":"Summarise {{text}}","system","outputMode":"text|json","targetField"}.`,
	"transformation:api_lookup":      "Fetches data from an HTTP API and merges it into the record.",
	"transformation:audit":           "Adds execution metadata (workflow, time, node) to the record.",
	"transformation:char_map":        "String normalisation: upper, lower, trim.",
	"transformation:data_conversion": "Explicit type casting of fields (string, number, date...).",
	"transformation:db_lookup":       "Looks up rows in a database (a configured source) and merges them into the record.",
	"transformation:decrypt":         "Decrypts fields encrypted by an encrypt node.",
	"transformation:dq_scorer":       "Scores data completeness and quality.",
	"transformation:encrypt":         "Encrypts named fields with AES-256-GCM.",
	"transformation:execute_sql":     "Runs an action SQL statement per record against a configured source.",
	"transformation:fanout":          "Splits an array into one record per item (same as foreach).",
	"transformation:field_diff": `For a change event, lists the columns that differ between the before- and ` +
		`after-image as {"col":{"old","new"}}. config: {"targetField":"changes","ignoreColumns":"updated_at",` +
		`"onlyChanges":false,"dropUnchanged":false}.`,
	"transformation:flatten": `Turns nested objects into one level of joined keys ({"a":{"b":1}} -> {"a_b":1}). ` +
		`config: {"separator":"_","maxDepth":"","arrays":"index|keep","field","targetField"}; no field flattens the whole record.`,
	"transformation:geo": `Geometry on coordinates in the record, no geocoding. config: {"operation":"distance",` +
		`"lat1Field","lon1Field","lat2Field","lon2Field","unit":"km|mi|m","targetField":"distance"} or ` +
		`{"operation":"within","latField","lonField","polygon":"<GeoJSON Polygon or MultiPolygon>","targetField":"inside"}.`,
	"transformation:parse_field": `Parses a text field into structure. config: {"field","format":"json|csv|xml|kv",` +
		`"targetField","delimiter","headers","hasHeader","pairDelimiter","kvSeparator"}. csv gives an array of rows.`,
	"transformation:reference_lookup": `Enriches from a CSV, TSV or Excel file held in memory and re-read when it ` +
		`changes. config: {"filePath":"<uploaded file path>","sheet","keyColumn":"code","keyField":"country_code",` +
		`"columns":"name,region","targetField","onMiss":"passthrough|fail|default","defaultValue"}.`,
	"transformation:template_render": `Renders a Go text/template over the record's fields into a field. config: ` +
		`{"template":"Hello {{.name}}","targetField":"rendered","strict":false}.`,
	"transformation:unflatten": `Rebuilds nested objects from joined keys ({"a_b":1} -> {"a":{"b":1}}); the ` +
		`inverse of flatten with the same config.`,
	"transformation:filter_data":       `Keeps or drops records by condition. config: {"conditions":"[{\"field\":\"status\",\"operator\":\"eq\",\"value\":\"paid\"}]"}.`,
	"transformation:foreach":           `Expands a list onto the same record. config: {"arrayPath":"items"}.`,
	"transformation:fuzzy_lookup":      "Approximate string matching against a reference set.",
	"transformation:join":              "Joins the record with data held in the state store.",
	"transformation:lua":               "Custom logic in a Lua script.",
	"transformation:mapping":           "Maps and reshapes fields into a new structure.",
	"transformation:mask":              "Masks or hashes sensitive values (PII).",
	"transformation:multicast":         "Clones the record to several branches.",
	"transformation:panmail_providers": "Lists a panmail tenant's email providers.",
	"transformation:pivot":             "Rotates rows into columns.",
	"transformation:rate_limit":        "Throttles the message flow.",
	"transformation:row_count":         "Counts records into workflow state.",
	"transformation:sampling":          "Passes a percentage or every Nth record.",
	"transformation:scd":               "Slowly Changing Dimension handling.",
	"transformation:set":               `Adds or overrides fields. config: {"column.<field>":"<expression or literal>"}.`,
	"transformation:stat_validator":    "Detects statistical anomalies and drift in numeric fields.",
	"transformation:term_extraction":   "Extracts keywords from text.",
	"transformation:unpivot":           "Rotates columns into rows.",
	"transformation:validate":          "Validates records by condition (same rules as filter_data); failures are reported.",
	"transformation:validator":         "Validates required fields and formats.",
	"transformation:wasm":              "Runs a WebAssembly module.",
}
