# Hermod SSE Sink Example

This example demonstrates how to consume data from a Hermod **SSE Sink** using a simple HTML/JavaScript client.

## How it works

The SSE Sink in Hermod publishes events to a named stream. This client uses the browser's native `EventSource` API to connect to the `/streams/sse` endpoint and receive these events in real-time.

## Setup Instructions

1.  **Start Hermod**: Ensure the Hermod server is running (default: `http://localhost:4000`).
2.  **Configure SSE Sink**:
    *   Go to the Hermod UI.
    *   Create a new **Sink** of type `SSE`.
    *   Set the **Stream Name** to something unique, e.g., `test-stream`.
3.  **Create a Workflow**:
    *   Create a new workflow with any source (e.g., a Webhook or a Database).
    *   Add the SSE Sink you just created as a destination.
    *   Activate the workflow.
4.  **Run this Client**:
    *   Open `index.html` in your web browser.
    *   Enter the same **Stream Name** (`test-stream`).
    *   Enter the **Auth Token** if you configured one.
    *   Ensure the **Hermod Server URL** matches your running instance.
    *   Click **Connect**.
5.  **Send Data**: Trigger your source (e.g., send a POST request to your webhook). You should see the data appearing instantly in the events log below.

## Security

Hermod's SSE Sink supports two levels of security:

1.  **Auth Token**: If configured, clients must provide the token. This client supports passing it via the `token` query parameter, which is the most compatible way for native `EventSource`. Hermod also supports the `Authorization: Bearer <token>` header.
2.  **Origin Verification**: You can restrict which domains are allowed to connect to your stream by configuring **Allowed Origins** in the sink settings.

## SSE Endpoint Details

The client connects to:
`GET /streams/sse?stream={stream_name}&token={optional_token}`

Hermod sends events with the following fields:
*   `id`: The unique Hermod message ID.
*   `event`: The operation type (`create`, `update`, `delete`, `snapshot`).
*   `data`: The message payload (usually JSON).

## Browser Compatibility

This example uses `EventSource`, which is supported in all modern browsers. Note that `EventSource` does not natively support custom headers (like Authorization), so the `/streams/sse` endpoint in Hermod is currently designed for data orchestration and is separated from the management API.
