package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/config"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/executor"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/server"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/state"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Configure log level
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// Create executor
	var exec executor.Executor
	switch cfg.Executor {
	case "docker":
		exec, err = executor.NewDockerExecutor()
		if err != nil {
			slog.Error("failed to create docker executor", "error", err)
			os.Exit(1)
		}
		slog.Info("using docker executor")
	case "subprocess":
		exec = executor.NewSubprocessExecutor()
		slog.Info("using subprocess executor")
	default:
		slog.Error("unknown executor type", "executor", cfg.Executor)
		os.Exit(1)
	}

	// Create state store and register jobs from config
	store := state.NewStore()
	for _, jd := range cfg.Jobs.Jobs {
		name := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", cfg.ProjectID, cfg.Region, jd.Name)
		job := &state.Job{
			Name:    name,
			Image:   jd.Image,
			Command: jd.Command,
			Env:     jd.Env,
		}
		if job.Env == nil {
			job.Env = make(map[string]string)
		}
		store.SaveJob(job)
		slog.Info("registered job", "name", name, "image", jd.Image)
	}

	// Start gRPC server
	srv := server.New(store, exec, cfg.ProjectID, cfg.Region)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down...")
		srv.Stop()
	}()

	if err := srv.Start(cfg.Port); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
