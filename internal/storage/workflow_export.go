package storage

// WorkflowExportBundle packages a workflow along with its referenced dependencies
// (like sources and sinks) for easy export and import between Hermod instances.
type WorkflowExportBundle struct {
	Workflow Workflow `json:"workflow"`
	Sources  []Source `json:"sources,omitempty" omitzero:"true"`
	Sinks    []Sink   `json:"sinks,omitempty" omitzero:"true"`
}
