# prepareWorkflowNodes writes the nodes it is given

`Registry.prepareWorkflowNodes` (`internal/engine/registry/registry_workflow.go`)
replaces a node's `Config` with the transformer's prepared map and sets keys in
it (`_parsed_steps`, `_parsed_columns`, `sequential`). A `storage.Workflow`
passed by value still shares its `Nodes` slice and every `Config` map with the
caller, so calling it on someone else's `wf.Nodes` writes their workflow.

That was a data race (#343): with "replay dead letters on startup",
`drainDeadLetters` replays in a goroutine through `ReplayDeadLetter` while
`StartWorkflow`'s deferred `unroutedBranches(wf)` reads the same nodes. CI
caught it intermittently in `TestAWorkflowReplaysItsDeadLettersWhenItStarts`.

`ReplayDeadLetter` now calls `ownNodes` first (copy of the slice and each
top-level `Config`, which is as deep as the prepare writes);
`TestReplayLeavesTheCallersNodesAlone` guards it. The other callers
(`StartWorkflow`, `RunWorkflowOnce`, `SimulateWorkflow`) still prepare the
`wf.Nodes` they were handed in place; that is safe only while nothing else reads
that workflow concurrently, which has not been audited for each of their
callers. Any new caller that runs on a shared workflow, especially from a goroutine,
must copy first.
