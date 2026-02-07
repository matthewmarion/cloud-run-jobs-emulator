package executor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/state"
)

// DockerExecutorOpts configures the Docker executor.
type DockerExecutorOpts struct {
	// ForwardLogs streams container stdout/stderr to the emulator logger when true.
	ForwardLogs bool
	// Network is the Docker network to attach spawned containers to.
	// "auto" (default) will attempt to detect the network of the emulator's own
	// container. "host" uses host networking. Any other value is treated as a
	// network name to join.
	Network string
	// ExtraHosts is a list of host:ip mappings to inject into spawned containers
	// (equivalent to docker run --add-host). Useful for e.g.
	// "host.docker.internal:host-gateway" so containers can reach the Docker host.
	ExtraHosts []string
}

type DockerExecutor struct {
	client      *client.Client
	forwardLogs bool
	network     string // resolved network name (empty means host mode)
	extraHosts  []string
}

func NewDockerExecutor(opts DockerExecutorOpts) (*DockerExecutor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	netName := resolveNetwork(cli, opts.Network)

	return &DockerExecutor{client: cli, forwardLogs: opts.ForwardLogs, network: netName, extraHosts: opts.ExtraHosts}, nil
}

// resolveNetwork determines which Docker network spawned containers should join.
func resolveNetwork(cli *client.Client, configured string) string {
	switch configured {
	case "host":
		return ""
	case "", "auto":
		return detectOwnNetwork(cli)
	default:
		return configured
	}
}

// detectOwnNetwork inspects the emulator's own container to find the Docker
// network it belongs to. It uses the hostname (which Docker sets to the
// container ID by default). Returns "" if detection fails (e.g. not running
// in Docker).
func detectOwnNetwork(cli *client.Client) string {
	hostname, err := os.Hostname()
	if err != nil {
		slog.Debug("network auto-detect: cannot read hostname", "error", err)
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := cli.ContainerInspect(ctx, hostname)
	if err != nil {
		slog.Debug("network auto-detect: cannot inspect own container", "hostname", hostname, "error", err)
		return ""
	}

	// Prefer the first non-default bridge network (compose networks, custom
	// networks, etc.). Fall back to any network found.
	var fallback string
	for name := range info.NetworkSettings.Networks {
		if name == "bridge" || name == "host" || name == "none" {
			continue
		}
		slog.Info("network auto-detect: found network", "network", name)
		return name
	}
	if fallback == "" {
		for name := range info.NetworkSettings.Networks {
			fallback = name
			break
		}
	}
	if fallback != "" {
		slog.Info("network auto-detect: using fallback network", "network", fallback)
	} else {
		slog.Debug("network auto-detect: no networks found on own container")
	}
	return fallback
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

	logger.Info("creating container", "network", e.networkDescription())

	hostCfg := &container.HostConfig{
		ExtraHosts: e.extraHosts,
	}
	var netCfg *network.NetworkingConfig

	if e.network != "" {
		// Attach to the specified network so the container can resolve
		// other services (e.g. host.docker.internal, compose services).
		hostCfg.NetworkMode = container.NetworkMode(e.network)
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				e.network: {},
			},
		}
	} else {
		hostCfg.NetworkMode = "host"
	}

	resp, err := e.client.ContainerCreate(ctx, &container.Config{
		Image: exec.Job.Image,
		Cmd:   exec.Job.Command,
		Env:   envSlice,
	}, hostCfg, netCfg, nil, "")
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
	rc, err := e.client.ContainerLogs(ctx, containerID, container.LogsOptions{
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

func (e *DockerExecutor) networkDescription() string {
	if e.network == "" {
		return "host"
	}
	return e.network
}

func (e *DockerExecutor) Cancel(exec *state.Execution) error {
	if exec.ContainerID == "" {
		return fmt.Errorf("no container ID for execution %s", exec.Name)
	}
	ctx := context.Background()
	return e.client.ContainerStop(ctx, exec.ContainerID, container.StopOptions{})
}
