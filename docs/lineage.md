# Data lineage and impact

Hermod builds a lineage graph from your saved workflows and the sources, sinks and ML models they use. It answers one question: **what breaks if I change this source?** Pick a table, topic, file, endpoint or model and Hermod lists every workflow and asset downstream of it, nearest first, with the path to each one.

Open it from **Data Lineage** in the sidebar. The page shows the vhost selected at the top of the app. With **All** selected, it shows every vhost you have access to.

## What the graph holds

There are two kinds of node:

- **Assets.** These are the things workflows read and write: a database table, a Kafka topic, an S3 prefix, a file, an HTTP endpoint, a search index, an ML model or dataset.
- **Workflows.**

Edges point the way data flows. An asset a workflow reads points at the workflow, and the workflow points at each asset it writes. Two workflows are connected when one writes an asset the other reads. This holds even when each workflow uses its own source or sink record. For example, if workflow A writes the Kafka topic `orders` through sink *orders out* and workflow B reads it through source *orders in*, both records point at one asset, `topic:kafka://kafka:9092/orders`. Changing what A writes then shows up as an impact on B.

The graph comes from stored configuration only. Each asset is identified by the same config keys the engine reads to connect to it. Nothing is inferred from names.

| Where it comes from | What it adds |
| --- | --- |
| Source node | The source's tables (`tables`), or its database when it runs a query; its topic, subject, stream or queue; its file, bucket or URL; its inbound path (webhook, chat, form, GraphQL, gRPC) |
| Sink node | The sink's table, collection, topic, stream, queue, index, bucket prefix, file, URL, dataset or entity. A `failover` or `txgroup` sink adds the assets of its member sinks |
| `db_lookup` | The looked-up table in the database of its `sourceId` (`looks_up`) |
| `api_lookup`, `reference_lookup` | The endpoint at `url` and the file at `filePath` (`looks_up`) |
| `execute_sql` | The database of its `sourceId` (`writes`). The statement itself is free SQL and is not parsed |
| `scd` | `targetTable` and `historyTable` in the database of `targetSourceId` (`writes`) |
| `ml_predict` | The model of the workflow's vhost (`predicts`) |
| `ml_train` | Its dataset (`reads`) and its model (`trains`). If `sourceId` is set, also that database (`reads`) and the dataset it refills (`writes`) |
| `ai_retrieve` | The pgvector table at `connectionString` + `table`, or the Pinecone index at `indexHost` + `namespace` (`retrieves`) |
| Pipeline steps, agent read tools | The same as each step or tool's own transformer |
| ML model with a retraining policy | Its dataset feeds the model (`trains`) |
| `batch_sql` source | The database of its delegate source (`source_id`). The queries are not parsed |

Some systems are the same database under different connector names. Postgres, YugabyteDB and pgvector share a family, as do MySQL and MariaDB. This is because the factory opens the same kind of connection for each.

### Assets known only by their record

Some connectors have no location Hermod can derive. Examples are a cron trigger, an SMTP or panmail sink, stdout, a Slack sink set up only with a webhook URL, a connection string that is a secret template, and any connector type lineage does not read. These are shown as a **connector** asset named after their source or sink record. Because they have no shared location, they cannot connect two workflows. The page counts them in a note under the asset picker.

### Credentials never appear

A locator keeps the host, port, database and object name and nothing else. User info (`user:password@`), query strings (`?password=`, `?api_key=`) and every other config value are dropped. E-mail recipients are not shown either.

## Who sees what

Lineage follows the same rule as the workflow list:

- An **Administrator** sees every vhost.
- Anyone else sees only the vhosts their account names.

Records outside your scope are removed **before** the graph is built. If a workflow in another vhost reads the same table as yours, it does not show up in your impact list. Asking for an asset outside your scope returns *not found*, the same answer as for an asset that does not exist.

## API

Both routes need a session (cookie or `Authorization: Bearer`). They are reads, open to every role.

```
GET /api/vhosts/{vhost}/lineage
GET /api/vhosts/{vhost}/lineage/impact?node=<node id>
```

`{vhost}` is a vhost name, or `all` for every vhost you may see.

The graph response:

```json
{
  "vhost": "tenant-a",
  "nodes": [
    { "id": "table:postgres://db:5432/shop/public.orders", "kind": "table", "label": "public.orders",
      "system": "postgres", "locator": "db:5432/shop", "resolved": true,
      "refs": [{ "kind": "source", "id": "pg", "name": "orders db", "type": "postgres" }] },
    { "id": "workflow:capture", "kind": "workflow", "label": "capture orders", "resolved": true,
      "vhost": "tenant-a", "active": true }
  ],
  "edges": [
    { "from": "table:postgres://db:5432/shop/public.orders", "to": "workflow:capture",
      "relation": "reads", "workflow_id": "capture", "node_id": "s" }
  ],
  "summary": { "workflows": 1, "assets": 1, "unresolved": 0, "edges": 1 }
}
```

Node ids have the form `kind:system://locator/object`, or `workflow:<id>` for a workflow. Treat them as opaque, and URL-encode one when you pass it as `node`.

The impact response lists every node downstream of `node`, nearest first, with the shortest `path` from the root and the `relation` of the last edge on it:

```json
{
  "vhost": "tenant-a",
  "root": { "id": "table:postgres://db:5432/shop/public.orders", "...": "..." },
  "impacted": [
    { "node": { "id": "workflow:capture", "...": "..." }, "depth": 1,
      "path": ["table:postgres://db:5432/shop/public.orders", "workflow:capture"], "relation": "reads" }
  ],
  "workflows": 1,
  "assets": 0
}
```

| Status | Meaning |
| --- | --- |
| 400 | `node` is missing, or longer than 2048 characters |
| 401 | No session |
| 403 | You do not have access to the vhost in the path |
| 404 | No such asset or workflow in your scope |
| 503 | Setup has not opened the metadata database yet |

The schema-change analysis (`POST /api/ai/analyze-schema`) also returns `downstream` when you have access to the vhost of the workflow you name. It has the same shape as the impact response, rooted at that workflow.

`GET /api/infra/lineage`, the dashboard's flat list of source → workflow → sink pairs, is also limited to the vhosts you may see.

## Limits

- Lineage is computed when you ask for it, from up to 50,000 workflows, sources and sinks per scope. Above that the request fails and asks you to pick a single vhost.
- SQL text is not parsed. A query-based source, `execute_sql` and `batch_sql` count as the whole database, not individual tables.
- Table names must match as written. `orders` and `public.orders` are different assets, and so are hosts written differently (`db` and `db.internal`).
- Only `ListMLModels`-capable backends (the SQL stores and MongoDB) contribute models with no workflow using them yet. A model a workflow predicts with always appears.
- Inside the Schema Registry, the AI Evolution tab sends a fixed workflow id. It does not show `downstream` until it lets you choose a workflow.
