package factory

import "slices"

// dlqRecoveryCapableSinkTypes are the sink types that can also be read back as
// a source, which is what "Prioritize DLQ on startup" and the Drain DLQ button
// both need: the engine wraps the dead-letter sink in a PrioritySource and
// reads parked messages out of it again.
//
// The editor used to keep its own copy of this list. It had drifted in both
// directions: four types it advertised as recovery-capable were not sources at
// all, so the checkbox was offered and StartWorkflow then refused the
// workflow; and nine types that are both sink and source were missing, so the
// editor greyed the checkbox out and the feature was simply unreachable for
// them. The list lives here now, beside the switches that decide it, and
// TestDLQRecoveryListMatchesFactory fails if either switch moves without it.
var dlqRecoveryCapableSinkTypes = []string{
	"cassandra",
	"clickhouse",
	"discord",
	"dynamics365",
	"eventstore",
	"facebook",
	"file",
	"ftp",
	"googlesheets",
	"http",
	"instagram",
	"kafka",
	"linkedin",
	"mariadb",
	"metis",
	"mongodb",
	"mqtt",
	"mssql",
	"mysql",
	"nats",
	"oracle",
	"postgres",
	"rabbitmq",
	"rabbitmq_queue",
	"redis",
	"s3",
	"sap",
	"slack",
	"sqlite",
	"tiktok",
	"twitter",
	"websocket",
	"yugabyte",
}

// DLQRecoveryCapableSinkTypes returns the sink types usable as a dead-letter
// sink that the engine can also drain. The result is a copy: callers sort and
// filter it.
func DLQRecoveryCapableSinkTypes() []string {
	return slices.Clone(dlqRecoveryCapableSinkTypes)
}

// SupportsDLQRecovery reports whether a dead-letter sink of this type can be
// read back for PrioritizeDLQ or an on-demand drain.
func SupportsDLQRecovery(sinkType string) bool {
	return slices.Contains(dlqRecoveryCapableSinkTypes, sinkType)
}
