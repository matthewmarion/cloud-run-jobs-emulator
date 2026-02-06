package executor

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/state"
)

type SubprocessExecutor struct{}

func NewSubprocessExecutor() *SubprocessExecutor {
	return &SubprocessExecutor{}
}

func (e *SubprocessExecutor) Run(execution *state.Execution, env map[string]string) {
	logger := slog.With("execution", execution.Name)

	if len(execution.Job.Command) == 0 {
		logger.Error("no command specified for job")
		execution.Status = state.StatusFailed
		execution.ErrorMessage = "no command specified"
		execution.FailedCount = 1
		execution.CompletionTime = time.Now()
		return
	}

	cmd := exec.Command(execution.Job.Command[0], execution.Job.Command[1:]...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logger.Info("starting subprocess", "command", execution.Job.Command)

	if err := cmd.Run(); err != nil {
		logger.Error("subprocess failed", "error", err)
		execution.Status = state.StatusFailed
		execution.FailedCount = 1
		execution.ErrorMessage = err.Error()
	} else {
		logger.Info("subprocess completed successfully")
		execution.Status = state.StatusSucceeded
		execution.SucceededCount = 1
	}
	execution.CompletionTime = time.Now()
}

func (e *SubprocessExecutor) Cancel(exec *state.Execution) error {
	return fmt.Errorf("cancel not supported for subprocess executor")
}
