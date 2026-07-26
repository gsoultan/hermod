# Hermod Project Agents (AGENTS.md)

This project uses **Agent Skills** via [skills.sh](https://skills.sh) to ensure optimal task execution.

## 🚀 Skills Integration (Mandatory)

Every time a task begins, you MUST execute the following workflow to choose the optimal skills:

1.  **List Active Skills**: `rtk npx skills list`
2.  **Search for Domain Skills**: If the task involves specific technologies, search for them:
    - `rtk npx skills search <tech>` (e.g., `react`, `go`, `protobuf`, `mantine`, `tanstack`)
3.  **Install Best Match**: Install the most relevant skill for the project scope:
    - `rtk npx skills add <owner/repo@skill> --project`
4.  **Optimal Selection Rule**: Favor skills with high install counts and official laboratory origins (e.g., `vercel-labs`, `bufbuild`).

### 📦 Recommended Skills for Hermod (Installed)
- **Backend (Go)**: `0xbigboss/claude-code@go-best-practices`
- **Frontend (React)**: `vercel-labs/agent-skills@vercel-react-best-practices`
- **Protobuf/gRPC**: `bufbuild/claude-plugins@protobuf`
- **UI (Mantine)**: `itechmeat/llm-code@mantine-dev`
- **State Management**: `lobehub/lobehub@zustand`
- **Forms**: `tanstack-skills/tanstack-skills@tanstack-form`
- **Query/Routing**: `deckardger/tanstack-agent-skills@tanstack-query-best-practices`, `tanstack-router-best-practices`
- **Agent Rules**: `netresearch/agent-rules-skill@agent-rules`
- **Token Efficiency**: `juliusbrussee/caveman@caveman` (Intensity: Ultra)

## 🏗️ Architecture & Precedence

1. **Happy Path**: Follow the domain-based organization in `internal/`.
2. **Project Guidelines**: Refer to `.junie/GUIDELINES.md` for detailed coding standards.
3. **Layered Pattern**: `Transports` → `Middlewares` → `Endpoints` → `Services` → `Usecases` → `Repositories`.
4. **Externalization**: All SQL MUST be in `.sql` files and embedded using `//go:embed`.
5. **Postgres MCP**: Use the integrated Postgres MCP server for schema discovery and data validation (see `.junie/POSTGRES_MCP.md`).
6. **RabbitMQ MCP**: Use the integrated RabbitMQ MCP server for message queue inspection and data flow validation (see `.junie/RABBITMQ_MCP.md`).
7. **Token Efficiency**: Always prefix terminal commands with `rtk`. Integrate **Caveman Code Ultra** for all communication to minimize token usage (~75% savings).

## 🛠️ Startup Checklist
- [ ] Run `rtk npx skills list`
- [ ] Identify and load relevant domain skills via `skills.sh`
- [ ] Check `CLAUDE.md` for build/test commands
- [ ] Start Graphify and detect codebase changes
- [ ] Review Serena memories: `rtk list_memories`
- [ ] Verify Postgres MCP connection: `postgres_list_tables`
- [ ] Verify RabbitMQ MCP connection: `rabbitmq_list_queues`
- [ ] Activate Caveman Ultra: `/caveman ultra`
