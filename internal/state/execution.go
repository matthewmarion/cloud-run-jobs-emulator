package state

import "time"

type ExecutionStatus int

const (
	StatusPending ExecutionStatus = iota
	StatusRunning
	StatusSucceeded
	StatusFailed
	StatusCancelled
)

func (s ExecutionStatus) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusRunning:
		return "RUNNING"
	case StatusSucceeded:
		return "SUCCEEDED"
	case StatusFailed:
		return "FAILED"
	case StatusCancelled:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}

// Execution represents a single job execution.
type Execution struct {
	// Full resource name: projects/{project}/locations/{location}/jobs/{job}/executions/{execution}
	Name           string
	Job            *Job
	Status         ExecutionStatus
	StartTime      time.Time
	CompletionTime time.Time
	SucceededCount int32
	FailedCount    int32
	ErrorMessage   string
	ContainerID    string // Docker container ID, used for cancellation
}
