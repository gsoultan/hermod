package registry_test

// This file lives in the external test package (registry_test) so it can
// blank-import the node-executor packages without creating an import cycle
// (the executor packages import the registry package). Importing the aggregator
// runs every executor's init(), registering the "router", "condition",
// "transformation", "switch", etc. executors into the shared registry. Without
// this, the internal registry tests that exercise runWorkflowNode would route
// through the empty-executor fallback and observe empty branches / untransformed
// messages.
import (
	_ "github.com/user/hermod/internal/engine/registry/nodes"
)
