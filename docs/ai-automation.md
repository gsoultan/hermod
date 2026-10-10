# AI automation

Hermod workflows can call language models from Anthropic (Claude), OpenAI (ChatGPT), Google (Gemini),
DeepSeek, Mistral, Groq, OpenRouter, Together, xAI, a local Ollama, or any OpenAI-compatible endpoint.
This page covers the AI nodes, the agent, retrieval, run history, the MCP server, the workflow builder and
self-healing proposals.

## Connections and keys

Every AI node takes the same connection settings:

| Setting | Meaning |
|---|---|
| `provider` | `anthropic` (alias `claude`), `openai` (alias `chatgpt`), `gemini` (alias `google`), `deepseek`, `mistral`, `groq`, `openrouter`, `together`, `xai`, `ollama`, `openai_compatible` |
| `model` | The provider's model id. `anthropic` defaults to `claude-opus-5-5`; the others need one. |
| `apiKey` | A vhost secret reference: `{{secret("ANTHROPIC_API_KEY")}}`. |
| `baseUrl` | Required for `openai_compatible`; optional elsewhere. |
| `fallbackProvider`, `fallbackModel`, `fallbackApiKey` | Used when the primary is unavailable (rate limit, server error, refusal), never for a bad request. |
| `maxConcurrency`, `maxTokens`, `temperature`, `timeout` | Per-node limits. `timeout` defaults to 60s. |

`apiKey`, `baseUrl` and the fallback key are resolved against the vhost's secrets only. Message data cannot
choose them, so a record cannot redirect a call or pick a credential. A key typed in as plain text works, but
validation warns about it and an exported workflow has it replaced with `[REDACTED]`.

Before data leaves Hermod, `inputFields` limits which fields are sent, `maskFields` replaces named fields with
`[REDACTED]`, and `maskPII` masks card numbers, US social security numbers, IP addresses, emails and phone numbers. Calls retry on rate
limits and server errors with backoff, honouring `Retry-After`.

## Nodes

| Node | What it does |
|---|---|
| `ai_prompt` | Sends a prompt (with `{{.field}}` placeholders and, optionally, the record) and writes the answer to `targetField` as text, or as an object when `outputMode` is `json`. |
| `ai_extract` | Extracts fields against a JSON Schema. A reply that does not validate is retried once with the validation errors. |
| `ai_embed` | Writes an embedding vector of a field or a template. Not available for Anthropic. |
| `ai_classify` | Picks one of the configured labels and routes the record down the edge with that label. Below `threshold` confidence, or for a label it was not offered, it takes the `unsure` edge. |
| `ai_retrieve` | Embeds a query and returns the `topK` closest documents from pgvector or Pinecone. |
| `ai_agent` | Pursues a goal with a bounded loop over the tools it is given (see below). |

The editor's AI palette also has Summarize and Translate, which are `ai_prompt` with a preset prompt.

### Conversation memory

For a chat endpoint, use a webhook source in sync reply mode. Send `X-Conversation-Id` (1–128 characters of
`A-Z a-z 0-9 . _ : -`); Hermod issues one when it is absent and echoes it back. Set `memory` on an
`ai_prompt` node:

```json
{"memory": {"conversationField": "conversation_id", "maxTurns": 10, "ttl": "24h"}}
```

Turns are kept in the engine's state store, so memory needs `StateStore` configured. Each turn is capped at
4 KiB and a conversation at 64 KiB.

## The agent

`ai_agent` takes a `goal`, an optional `system` prompt and a list of `tools`. Limits: `maxSteps` (default 5,
at most 20), `maxTotalTokens` (default 50,000, at most 1,000,000), `timeout` (default 2m, at most 10m). The
answer goes to `ai_agent_answer` and the full transcript (steps, tool calls, results, token usage) to
`ai_agent_transcript`, which the Approvals page and the debugger display.

Tool kinds:

| Kind | Does | Approval |
|---|---|---|
| `db_lookup`, `api_lookup`, `ai_retrieve` | Runs that lookup with the node's fixed `config`; the model fills only the declared `parameters`. | Only when marked `write: true`. |
| `ml_predict` | Runs the vhost's model named in `config.model` on the declared `parameters`, which must be the model's features. See [ml.md](ml.md#mcp-clients). | Only when marked `write: true`. |
| `sink` | Writes to a sink node (`nodeId`) of the same workflow. | Always, unless `requireApproval: false`. |
| `mcp` | Calls one named `tool` of a remote MCP server (`server.url`, `server.headers`). | Unless the tool sets `write: false` **and** the server marks it read-only, or `requireApproval: false`. |

What the agent can do is fixed by the workflow, not by the data:

- The model can call only the tools in the node's list. Any other call gets an error result and runs nothing.
- Record data reaches the model only as data inside the user turn, never in the system prompt.
- A tool call that needs approval suspends the record and creates an approval showing the exact call. It runs
  once after approval and never after rejection. An approval can be decided once, and only by users with
  access to the workflow's vhost.

## Run history and manual runs

| Endpoint | Role | Purpose |
|---|---|---|
| `GET /api/workflows/{id}/executions?limit=&before=` | Viewer | Recent runs with status, duration, step and error counts and AI token totals. |
| `GET /api/workflows/{id}/executions/{run_id}` | Viewer | One run with each step's output. |
| `POST /api/workflows/{id}/executions/{run_id}/replay` | Editor | Runs the recorded input again. |
| `POST /api/workflows/{id}/run` | Editor | Runs the workflow once with `{"message": {...}, "source_node_id": "..."}`. Sinks really write. |

Live runs appear when the workflow's trace sample rate is above 0; manual runs are always traced.

Any node can retry on its own: `config.retry = {"maxAttempts": 3, "backoff": "1s", "maxBackoff": "30s",
"on": "<error regexp>"}`. After the last attempt the record goes to the dead-letter queue as before.

AI calls are counted in `hermod_ai_calls_total`, `hermod_ai_tokens_total` and
`hermod_ai_call_duration_seconds`, labelled by provider, model and outcome. Prompts and keys are never labels.

## Templates

`examples/templates/` has three AI starters: support triage (classify, extract, draft a reply, approval,
send), invoice extraction, and a CDC anomaly explainer. Each reads its key from `{{secret("ANTHROPIC_API_KEY")}}`.

## MCP server

Hermod serves its workflows to Claude, ChatGPT and other MCP clients at `/api/mcp` (Streamable HTTP). Clients
authenticate like any API caller. Only workflows tagged `mcp` are exposed, and only within the caller's vhosts.

| Tool | Returns |
|---|---|
| `list_workflows` | The exposed workflows and whether each can be run and replies with a result. |
| `get_workflow_status` | Status, processed and error counts, lag. |
| `run_workflow` (Editor) | Runs the workflow with `input`. A webhook source in sync reply mode returns the workflow's reply. |
| `predict_<model>` | One prediction from a model an Editor exposed to MCP, with the model's features as arguments. Read-only. See [ml.md](ml.md#mcp-clients). |

## Describe an automation

`POST /api/ai/build-workflow` (Editor) turns a description into a draft workflow using a connection you name
(the key must be a `{{secret("NAME")}}` reference). The draft only uses node types this build can run and the
vhost's own sources and sinks, is checked by the workflow validator, and is returned with its issues. It is
never saved or started.

## Self-healing proposals

When a workflow keeps failing, Hermod proposes a configuration change (for example more retries) instead of
making it. Proposals are listed at `GET /api/workflows/{id}/proposals` and applied with
`POST .../proposals/{pid}/approve` (Editor), which checks the workflow has not changed since, validates the
result and records a new version that can be rolled back. Safe Mode and batch-size changes are recommended,
never applied.

Mapping suggestions are off unless `HERMOD_SELF_HEALING_AI_PROVIDER` is set (with `_MODEL`, `_BASE_URL` and
`_API_KEY`, the key as a secret reference); sample values are PII-masked before they are sent.
