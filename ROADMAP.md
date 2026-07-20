# Hermod Development Roadmap & Ideas

This document outlines the planned features, development directions, and future ideas for the Hermod platform to transform it from a "Data Mover" into a comprehensive "Data Orchestration Platform."

## 🚀 Enhanced Control Flow & Reliability (Implemented ✅)
- **Sequential Sink Chaining**: Introduced sequential execution for sink nodes. Currently, sinks execute in parallel. Sequential chaining allows Sink B to trigger only if Sink A returns a success.
- **Sinks as Transformers**: Sequential sinks return their output message to the next node in the pipeline.
- **Circuit Breaker with Intelligent Recovery**: Implemented a Circuit Breaker node that monitors failures and switches to a failure branch when a threshold is reached. Supports CLOSED, OPEN, and HALF_OPEN states.
- **Stateful Event Correlation (Join/Zip)**: Implemented a Join node that waits for messages from multiple sources based on a common key and merges them.

## 🤖 AI & Data Intelligence
- **Semantic Routing**: Leverage LLM integrations to route data based on **intent or sentiment** (e.g., "If the message sounds like a complaint, route to the Urgent Support sink").
- **Autonomous Schema Evolution**: When a source schema changes (e.g., a new column in Postgres CDC), Hermod could automatically update the downstream SQL sink schema or alert the user with a "Smart Mapping" suggestion.
- **Natural Language Workflow Generation**: A "Prompt to Workflow" feature where users can describe their needs in natural language to generate the DAG automatically.

## 🏢 Enterprise Governance & Observability
- **Data Lineage & Impact Heatmaps**: Provide a global view of data flows across all workflows. Highlight affected downstream sinks when a source is modified.
- **Hermod Edge Orchestration**: Lightweight Hermod instances for local filtering, masking, and pre-processing before sending data to a central Hermod cluster.

## ✨ "Zero-Entry" Ease of Use
- **Live "Dry-Run" Preview**: A split-pane view in the Editor that displays exactly how sample messages are transformed at each node in real-time as configurations change.
- **Workflow "Blueprints" Marketplace**: An integrated store where users can download and share pre-built templates for common scenarios (e.g., "GDPR Compliance Pipeline," "E-commerce Sync").

## 🔗 Infrastructure & Connectivity
- **Expanded Connector Library**: Continued development of native, high-performance connectors for enterprise systems.
- **Improved Atomic Delivery**: Enhanced support for Two-Phase Commit (2PC) and multi-sink consistency across a wider range of sink types.
