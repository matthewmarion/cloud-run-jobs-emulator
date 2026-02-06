package executor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/state"
)

type DockerExecutor struct {
	client *client.Client
}

func NewDockerExecutor() (*DockerExecutor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &DockerExecutor{client: cli}, nil
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

func (e *DockerExecutor) Cancel(exec *state.Execution) error {
	if exec.ContainerID == "" {
		return fmt.Errorf("no container ID for execution %s", exec.Name)
	}
	ctx := context.Background()
	return e.client.ContainerStop(ctx, exec.ContainerID, container.StopOptions{})
}
