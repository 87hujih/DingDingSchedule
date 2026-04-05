package agent

type ToolOutcome struct {
	OK        bool
	ErrorCode string
	Message   string
	Retryable bool
	Data      any
}

type TaskResult struct {
	Outcome      ToolOutcome
	Reply        string
	KeepTaskOpen bool
}

type SlotParseResult struct {
	Slots map[string]string
}

type ValidationResult struct {
	Valid        bool
	MissingSlots []string
}
