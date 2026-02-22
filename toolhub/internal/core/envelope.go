package core

// ToolEnvelope is the standard response wrapper for all tool calls.
// Used by both HTTP and MCP transports.
type ToolEnvelope struct {
	OK     bool       `json:"ok"`
	Meta   ToolMeta   `json:"meta"`
	Result any        `json:"result"`
	Error  *ToolError `json:"error,omitempty"`
}

// ToolMeta contains audit metadata for a tool call.
type ToolMeta struct {
	RunID        string       `json:"run_id"`
	ToolCallID   string       `json:"tool_call_id"`
	EvidenceHash string       `json:"evidence_hash"`
	DryRun       bool         `json:"dry_run"`
	QAArtifacts  *QAArtifacts `json:"qa_artifacts,omitempty"`
}

type QAArtifacts struct {
	StdoutArtifactID string `json:"stdout_artifact_id,omitempty"`
	StderrArtifactID string `json:"stderr_artifact_id,omitempty"`
	ReportArtifactID string `json:"report_artifact_id,omitempty"`
}

// ToolError represents a tool-level error (distinct from transport errors).
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
