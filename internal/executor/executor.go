package executor

import "github.com/mattkinnersley/cloud-run-jobs-emulator/internal/state"

// Executor runs a job execution.
type Executor interface {
	// Run executes a job with the given environment variables.
	// It updates the execution status upon completion.
	// This method is intended to be called in a goroutine.
	Run(exec *state.Execution, env map[string]string)

	// Cancel stops a running execution.
	Cancel(exec *state.Execution) error
}
