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

For a chat endpoint, use a [chat source](#chat-trigger), or a webhook source in sync reply mode. A webhook
caller sends `X-Conversation-Id` (1–128 characters of `A-Z a-z 0-9 . _ : -`); Hermod issues one when it is
absent and echoes it back. Set `memory` on an `ai_prompt` node:

```json
{"memory": {"conversationField": "conversation_id", "maxTurns": 10, "ttl": "24h"}}
```

Turns are kept in the engine's state store, so memory needs `StateStore` configured. Each turn is capped at
4 KiB and a conversation at 64 KiB.

## Chat trigger

A `chat` source receives one message of a conversation and answers with the workflow's reply. Put an
`ai_prompt` node with `memory` after it and the chat remembers what was said (the source sets
`conversation_id`, which is where memory looks by default). The source answers with the record field named by
`reply_field`, by default `ai_output`, which is where `ai_prompt` writes its answer.

Each source has its own path, `/api/chat/<name>`, and a `platform`:

| Platform | Receives | Authenticated by | Answers |
|---|---|---|---|
| `web` (default) | `POST /api/chat/<name>` with `{"conversation_id", "message", "user", "metadata"}` | `api_key`, sent as `X-API-Key`; or the public `widget_key`, sent as `?widget_key=` from an origin in `allowed_origins` | In the response |
| `slack` | Events API deliveries (`message`, `app_mention`) | `slack_signing_secret` (`X-Slack-Signature`, five-minute replay window) | `chat.postMessage` with `slack_bot_token`, in the thread the message was in |
| `telegram` | Bot API webhook updates (text messages) | `telegram_secret_token` (`X-Telegram-Bot-Api-Secret-Token`) | A `sendMessage` call in the webhook response; no bot token needed |

A chat source with no credential for its platform answers nobody. Any credential can be a `secret:NAME`
reference, read from the source's vhost secrets; a reference to a secret that does not exist counts as no
credential.

A web request is answered:

| Status | Body | When |
|---|---|---|
| 200 | `{"id", "status", "conversation_id", "reply"}` | The workflow finished: `delivered`, `completed` (no sink) or `filtered`. |
| 202 | `{"id", "status": "pending", "conversation_id"}` | `response_timeout` (default 30s, at most 5m) ran out. The message is still the workflow's; do not resend it. |
| 502 | `{"id", "status", "conversation_id", "error"}` | The workflow failed. The cause is in the run history, not in the answer. |
| 400, 401, 403, 404, 413, 429, 503 | `{"error"}` | A bad request, a wrong or missing credential, an origin not allowed, no such chat, a body over 16 KiB, the rate limit, or the workflow not running. |

`conversation_id` is optional; without one a new conversation starts and its id comes back to send next time.
`message` is at most 4 KiB, the size memory keeps of a turn. `rate_limit` (default 120) is messages per caller
IP per hour on the web platform. There is no poll endpoint for a `pending` answer, as for a sync webhook.

### The web widget

```html
<script src="https://hermod.example.com/api/chat/widget.js" async
        data-endpoint="https://hermod.example.com/api/chat/support"
        data-widget-key="PUBLIC_WIDGET_KEY"
        data-title="Support"></script>
```

The widget key is public: anyone can read it from the page, so it is accepted only with an `Origin` in the
source's `allowed_origins` (exact `scheme://host[:port]`, comma-separated; `*` is not accepted), and the answer
carries `Access-Control-Allow-Origin` for that origin only. It is not a Hermod credential and cannot call
anything else. A script outside a browser can send any `Origin`, so the origin list keeps the key from working
on other sites, and `rate_limit` and the model's own limits are what bound its cost. The script has no dependencies, uses no `eval`, inline styles or `innerHTML`, and posts a simple
`text/plain` request without cookies, so the embedding page needs only `script-src` and `connect-src` for the
Hermod host. The conversation lasts as long as the browser tab.

### Slack

Create a Slack app with the `chat:write` scope, set Event Subscriptions' Request URL to the source's URL and
subscribe to `message.im` (and `app_mention` for channels). Slack expects an answer within three seconds, so
the source acknowledges at once and posts the reply when the workflow has it. Slack's retries are acknowledged
and ignored, the bot's own messages and edits are ignored, and a message's conversation is
`slack:<team>:<channel>[:<thread>]`.

### Telegram

Register the source as the bot's webhook with a secret token:
`https://api.telegram.org/bot<token>/setWebhook?url=<source URL>&secret_token=<telegram_secret_token>`. A
conversation is `telegram:<chat id>`. An answer that is not ready within `response_timeout` is not sent.

## The agent

`ai_agent` takes a `goal`, an optional `system` prompt and a list of `tools`. Limits: `maxSteps` (default 5,
at most 20), `maxTotalTokens` (default 50,000, at most 1,000,000), `timeout` (default 2m, at most 10m). The
answer goes to `ai_agent_answer` and the full transcript (steps, tool calls, results, token usage) to
`ai_agent_transcript`, which the Approvals page and the debugger display.

Tool kinds:

| Kind | Does | Approval |
|---|---|---|
| `db_lookup`, `api_lookup`, `ai_retrieve` | Runs that lookup with the node's fixed `config`; the model fills only the declared `parameters`. | Only when marked `write: true`. |
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

## Budgets and the kill switch

Each vhost can limit what its AI nodes and agents spend, on the **AI Budget** page or through the API. A limit
of 0 (or none) is no limit.

- **Monthly budget** for the whole vhost, in tokens (input plus output), in cost, or both.
- **Workflow caps**: a monthly token or cost limit for one workflow, inside the vhost's.
- **Prices** per million input and output tokens, per model. Hermod holds no price list. Model `*` prices every
  model the list does not name, and a cost limit cannot be saved without it, so no call goes uncounted. The
  currency is only a label.
- **Kill switch**: while it is on, every AI call in the vhost is refused. It keeps the limits and the usage.

Months are calendar months in UTC. Usage is stored in the `ai_budgets` and `ai_usage` tables (SQL) or
collections (MongoDB). Every replica shares it, and it survives a restart. It is added atomically after each
call that answers.

**Enforcement.** Every model call made through the AI connections is checked against its vhost and workflow:
AI Prompt, Extract, Embed, Classify, Retrieve and the agent, embeddings included, and also the workflow builder and
self-healing mapping suggestions, which count against the vhost only. A refused call never reaches
the provider. It fails with an error that starts with `AI budget:` and names the limit (`ai_disabled`,
`vhost_tokens`, `vhost_cost`, `workflow_tokens`, `workflow_cost` or `budget_unavailable`), and the record takes
the node's error branch.

- **Fails closed.** If the budget cannot be read, calls in that vhost are refused (`budget_unavailable`) rather
  than let through.
- **Can overshoot.** A call is checked before it is made and counted after it answers. The call that crosses a
  limit is let through and only the next one is refused, so a month can end slightly over, by about one call
  per concurrently running node.
- **Takes up to 5 seconds to reach every replica.** Each replica reads a budget at most every 5 seconds. A
  change applies at once on the replica that saved it and within 5 seconds on the others; the kill switch
  included.
- **Remote workers** ask the control plane before each call and report usage afterwards
  (`POST /api/worker/ai/check` and `/api/worker/ai/usage`, worker tokens only). A worker that cannot reach it
  refuses the call.

Not covered: the legacy AI Enrichment and AI Mapper nodes and the `OPENAI_API_KEY`-based optimizer, which call
their provider directly rather than through an AI connection.

**Alerts.** When a scope (the vhost, or one capped workflow) passes 80% of a token or cost limit, Hermod logs
it, counts it in `hermod_ai_budget_warnings_total{vhost,scope,kind}`, and sends a WARN notification through the
notification channels. The alert is sent once per scope, limit kind and month across all replicas. A refused
call is counted in `hermod_ai_budget_blocked_total{vhost,limit}` and in `hermod_ai_calls_total` with outcome
`budget_exceeded`. When a limit is reached, an ERROR notification is sent at most once a minute per replica;
none is sent for the kill switch, which someone turned on deliberately.

| Endpoint | Role | Purpose |
|---|---|---|
| `GET /api/vhosts/{vhost}/ai/budget` | Viewer | The budget, this month's usage of the vhost and of each workflow. |
| `PUT /api/vhosts/{vhost}/ai/budget` | Editor | Replaces the budget: `disabled`, `monthly_tokens`, `monthly_cost`, `currency`, `prices`, `workflows`. |
| `PUT /api/vhosts/{vhost}/ai/kill-switch` | Editor | `{"disabled": true}` stops every AI call in the vhost; `false` lets them through again. The limits are kept. |

The caller must have access to the vhost. Both changes are written to the audit log. Deleting a vhost deletes
its budget and usage.

## Templates

`examples/templates/` has four AI starters: support triage (classify, extract, draft a reply, approval,
send), invoice extraction, a CDC anomaly explainer, and a chat assistant (chat source, AI Prompt with memory,
reply). Each reads its key from `{{secret("ANTHROPIC_API_KEY")}}`.

## MCP server

Hermod serves its workflows to Claude, ChatGPT and other MCP clients at `/api/mcp` (Streamable HTTP). Clients
authenticate like any API caller. Only workflows tagged `mcp` are exposed, and only within the caller's vhosts.

| Tool | Returns |
|---|---|
| `list_workflows` | The exposed workflows and whether each can be run and replies with a result. |
| `get_workflow_status` | Status, processed and error counts, lag. |
| `run_workflow` (Editor) | Runs the workflow with `input`. A webhook source in sync reply mode returns the workflow's reply. |

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
