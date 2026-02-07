package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type JobDefinition struct {
	Name    string            `yaml:"name"`
	Image   string            `yaml:"image"`
	Command []string          `yaml:"command"`
	Env     map[string]string `yaml:"env"`
	Resources struct {
		CPU    string `yaml:"cpu"`
		Memory string `yaml:"memory"`
	} `yaml:"resources"`
	Timeout string `yaml:"timeout"`
}

type JobsConfig struct {
	Jobs []JobDefinition `yaml:"jobs"`
}

type Config struct {
	Port                 string
	JobsFile             string
	Executor             string
	LogLevel             string
	ProjectID            string
	Region               string
	ForwardContainerLogs bool
	DockerNetwork        string
	Jobs                 *JobsConfig
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:                 getEnv("PORT", "8123"),
		JobsFile:             getEnv("JOBS_CONFIG", "./jobs.yaml"),
		Executor:             getEnv("EXECUTOR", "docker"),
		LogLevel:             getEnv("LOG_LEVEL", "info"),
		ProjectID:            getEnv("PROJECT_ID", "fake-project"),
		Region:               getEnv("REGION", "us-central1"),
		ForwardContainerLogs: getEnvBool("FORWARD_CONTAINER_LOGS", false),
		DockerNetwork:        getEnv("DOCKER_NETWORK", "auto"),
	}

	jobs, err := loadJobsConfig(cfg.JobsFile)
	if err != nil {
		return nil, fmt.Errorf("loading jobs config: %w", err)
	}
	cfg.Jobs = jobs

	return cfg, nil
}

func loadJobsConfig(path string) (*JobsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file is fine - jobs can be created via API
			return &JobsConfig{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg JobsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
