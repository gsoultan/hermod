# Hermod Management Platform UI

This is the frontend for the Hermod data integration platform. It is built with React, TypeScript, and Mantine, providing a drag-and-drop workflow editor and comprehensive management dashboard.

## Key Features

- **Drag-and-Drop Workflow Editor**: Design complex data pipelines visually using React Flow.
- **Real-time Monitoring**: Monitor message throughput, error rates, and system logs via WebSockets.
- **Pipeline Health Heatmaps**: Identify bottlenecks instantly with dynamic health-based node styling.
- **Message Trace Visualization**: Track individual messages through the DAG to see latency and mutations.
- **AI-Powered Editor**: Built-in configuration for AI Enrichment and AI Mapping nodes.
- **Hermod CLI Support**: Optimized for managing workflows that are exported/imported via the CLI.
- **Global Schema Registry**: Register and version data contracts (JSON Schema, Avro, Protobuf).
- **Enterprise Configuration**: Unified UI for setting up Secret Managers (Vault, AWS, Azure) and Global State Stores.

## Development

The UI is built using **Bun** and **Vite**.

1.  Navigate to the `ui` directory:
    ```bash
    cd ui
    ```
2.  Install dependencies:
    ```bash
    bun install
    ```
3.  Start the development server:
    ```bash
    bun run dev
    ```
    The UI will be available at `http://localhost:5173`. By default, it proxies API requests to `http://localhost:8080`.

## Production Build

To build the UI for production:
```bash
bun run build
```
The resulting assets will be in the `dist` directory. The Go backend can automatically build and embed these assets using the `--build-ui` flag.

## Build Verification

Ensure the UI builds and passes linting before submitting changes:
```bash
bun run build
bun run lint
```
