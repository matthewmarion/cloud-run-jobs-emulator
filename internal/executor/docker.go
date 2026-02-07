package executor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/docker/api/types"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/state"
)

// DockerExecutorOpts configures the Docker executor.
type DockerExecutorOpts struct {
	// ForwardLogs streams container stdout/stderr to the emulator logger when true.
	ForwardLogs bool
}

type DockerExecutor struct {
	client      *client.Client
	forwardLogs bool
}

func NewDockerExecutor(opts DockerExecutorOpts) (*DockerExecutor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &DockerExecutor{client: cli, forwardLogs: opts.ForwardLogs}, nil
}

// lineLogWriter buffers writes and logs each complete line to slog.
type lineLogWriter struct {
	logger *slog.Logger
	stream string
	buf    []byte
}

func (w *lineLogWriter) Write(p []byte) (n int, err error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := string(bytes.TrimSpace(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			w.logger.Info("container", "stream", w.stream, "line", line)
		}
	}
}

func (w *lineLogWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	line := string(bytes.TrimSpace(w.buf))
	w.buf = w.buf[:0]
	if line != "" {
		w.logger.Info("container", "stream", w.stream, "line", line)
	}
}

func (e *DockerExecutor) Run(exec *state.Execution, env map[string]string) {
	ctx := context.Background()
	logger := slog.With("execution", exec.Name, "image", exec.Job.Image)

	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	logger.Info("creating container")

	resp, err := e.client.ContainerCreate(ctx, &container.Config{
		Image: exec.Job.Image,
		Cmd:   exec.Job.Command,
		Env:   envSlice,
	}, &container.HostConfig{
		NetworkMode: "host",
	}, nil, nil, "")
	if err != nil {
		logger.Error("failed to create container", "error", err)
		exec.Status = state.StatusFailed
		exec.ErrorMessage = fmt.Sprintf("container create failed: %v", err)
		exec.FailedCount = 1
		exec.CompletionTime = time.Now()
		return
	}

	exec.ContainerID = resp.ID
	logger = logger.With("container_id", resp.ID)

	logger.Info("starting container")
	if err := e.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		logger.Error("failed to start container", "error", err)
		exec.Status = state.StatusFailed
		exec.ErrorMessage = fmt.Sprintf("container start failed: %v", err)
		exec.FailedCount = 1
		exec.CompletionTime = time.Now()
		// Clean up the created container
		_ = e.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
		return
	}

	if e.forwardLogs {
		go e.streamContainerLogs(ctx, resp.ID, logger)
	}

	statusCh, errCh := e.client.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("error waiting for container", "error", err)
			exec.Status = state.StatusFailed
			exec.ErrorMessage = fmt.Sprintf("container wait failed: %v", err)
			exec.FailedCount = 1
		}
	case result := <-statusCh:
		if result.StatusCode == 0 {
			logger.Info("container completed successfully")
			exec.Status = state.StatusSucceeded
			exec.SucceededCount = 1
		} else {
			logger.Warn("container failed", "exit_code", result.StatusCode)
			exec.Status = state.StatusFailed
			exec.FailedCount = 1
			exec.ErrorMessage = fmt.Sprintf("container exited with code %d", result.StatusCode)
		}
	}

	exec.CompletionTime = time.Now()

	// Clean up container
	_ = e.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
}

func (e *DockerExecutor) streamContainerLogs(ctx context.Context, containerID string, logger *slog.Logger) {
	rc, err := e.client.ContainerLogs(ctx, containerID, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		logger.Error("failed to attach container logs", "error", err)
		return
	}
	defer rc.Close()

	stdoutWriter := &lineLogWriter{logger: logger, stream: "stdout"}
	stderrWriter := &lineLogWriter{logger: logger, stream: "stderr"}

	_, _ = stdcopy.StdCopy(stdoutWriter, stderrWriter, rc)
	stdoutWriter.Flush()
	stderrWriter.Flush()
}

func (e *DockerExecutor) Cancel(exec *state.Execution) error {
	if exec.ContainerID == "" {
		return fmt.Errorf("no container ID for execution %s", exec.Name)
	}
	ctx := context.Background()
	return e.client.ContainerStop(ctx, exec.ContainerID, container.StopOptions{})
}
