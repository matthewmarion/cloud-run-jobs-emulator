# Cloud Run Jobs Emulator

A local emulator for the [Google Cloud Run Jobs v2 API](https://cloud.google.com/run/docs/reference/rpc/google.cloud.run.v2). Run and test Cloud Run Jobs locally without GCP infrastructure.

Works as a drop-in replacement with the official `google-cloud-run` client libraries. Inspired by [fake-gcs-server](https://github.com/fsouza/fake-gcs-server).

## Features

- Full gRPC implementation of the Cloud Run Jobs v2 API
- **Docker executor** — runs job containers locally using Docker
- **Subprocess executor** — runs commands directly without Docker
- Pre-register jobs via YAML config or create them via the API
- Environment variable overrides via `RunJobRequest.Overrides`
- Async execution with status polling via `GetExecution`

### Docker Compose

```yaml
services:
  cloud-run-emulator:
    image: ghcr.io/matthewmarion/cloud-run-jobs-emulator:latest
    ports:
      - "8123:8123"
    volumes:
      - ./jobs.yaml:/etc/emulator/jobs.yaml
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      JOBS_CONFIG: /etc/emulator/jobs.yaml
      LOG_LEVEL: debug
      # Optional: stream job container logs to the emulator (helps debug failures)
      # FORWARD_CONTAINER_LOGS: "true"
```

#### Networking

When running inside Docker Compose, the emulator **automatically detects** the Compose network and attaches spawned job containers to it. This means job containers can resolve other Compose service names out of the box — no extra configuration needed.

If auto-detection doesn't suit your setup, override it with `DOCKER_NETWORK`:

```yaml
environment:
  # Use a specific network
  DOCKER_NETWORK: my-custom-network
  # Or fall back to host networking (legacy behaviour)
  DOCKER_NETWORK: host
```

##### Reaching the Docker Host

If your job containers need to reach processes running directly on the host machine (e.g. a Python dev server on `localhost:8000`), add the `DOCKER_EXTRA_HOSTS` env var:

```yaml
environment:
  DOCKER_EXTRA_HOSTS: "host.docker.internal:host-gateway"
```

This injects `--add-host host.docker.internal:host-gateway` into every spawned container, so they can call `http://host.docker.internal:8000/...` to reach host-local services. Combine this with a `CALLBACK_URL` like `http://host.docker.internal:8000/callback` in your job config for local dev workflows where both Docker containers and bare-metal processes need to talk to each other.

#### GPU Passthrough

If your job containers need access to NVIDIA GPUs (e.g. for ML inference with PyTorch/TensorFlow), set `DOCKER_GPU`:

```yaml
environment:
  DOCKER_GPU: "true"
```

This adds `--gpus all` to every spawned container, exposing all host GPUs to the job. Requires the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html) to be installed on the Docker host.

> **Note:** `DOCKER_GPU` is a Docker-level setting on the *emulator* — it controls whether the host GPU hardware is visible inside spawned containers. This is separate from any app-level toggles like `PROCESSING_USE_GPU` that your application may read. Both must be set: the emulator needs `DOCKER_GPU=true` to expose the GPU, and your app needs its own flag to actually use it.

### From Source

```bash
go build -o cloud-run-jobs-emulator ./cmd/emulator/
JOBS_CONFIG=./jobs.yaml ./cloud-run-jobs-emulator
```

## Configuration

### Job Definitions (`jobs.yaml`)

Pre-register jobs so they're available immediately on startup:

```yaml
jobs:
  - name: my-job
    image: my-registry/my-image:latest
    command: ["python", "-m", "my_module.main"]
    env:
      ENVIRONMENT: local
      CALLBACK_URL: http://host.docker.internal:8000/callback
    timeout: 3600s
```

Jobs can also be created at runtime via the `CreateJob` API.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8123` | gRPC server port |
| `JOBS_CONFIG` | `./jobs.yaml` | Path to job definitions file |
| `EXECUTOR` | `docker` | Executor type: `docker` or `subprocess` |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `PROJECT_ID` | `fake-project` | Default GCP project ID |
| `REGION` | `us-central1` | Default region |
| `FORWARD_CONTAINER_LOGS` | `false` | When `true` (or `1`/`yes`/`on`), stream container stdout/stderr to the emulator logs. Useful for debugging failing jobs. |
| `DOCKER_NETWORK` | `auto` | Docker network for spawned job containers. `auto` detects the emulator's own network (e.g. the Compose network), `host` uses host networking, or pass an explicit network name. |
| `DOCKER_EXTRA_HOSTS` | _(none)_ | Comma-separated `host:ip` mappings injected into spawned containers (equivalent to `docker run --add-host`). Example: `host.docker.internal:host-gateway` lets job containers reach the Docker host. |
| `DOCKER_GPU` | `false` | When `true`, passes `--gpus all` to spawned containers, exposing host NVIDIA GPUs. Requires the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html) on the Docker host. |

## Client Setup

The `google-cloud-run` library doesn't auto-detect an emulator host, so you need to configure the client manually.

### Python

```python
import os
import grpc
from google.cloud import run_v2
from google.cloud.run_v2.services.jobs.transports import JobsGrpcTransport

emulator_host = os.environ.get("CLOUD_RUN_EMULATOR_HOST")

if emulator_host:
    channel = grpc.insecure_channel(emulator_host)
    transport = JobsGrpcTransport(channel=channel)
    client = run_v2.JobsClient(transport=transport)
else:
    client = run_v2.JobsClient()

# Use client as normal
operation = client.run_job(
    name="projects/fake-project/locations/us-central1/jobs/my-job",
    overrides=run_v2.RunJobRequest.Overrides(
        container_overrides=[
            run_v2.RunJobRequest.Overrides.ContainerOverride(
                env=[run_v2.EnvVar(name="PAYLOAD", value='{"key": "value"}')]
            )
        ]
    ),
)

execution_name = operation.metadata.name
```

### Go

```go
import (
    run "cloud.google.com/go/run/apiv2"
    runpb "cloud.google.com/go/run/apiv2/runpb"
    "google.golang.org/api/option"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

conn, _ := grpc.NewClient("localhost:8123",
    grpc.WithTransportCredentials(insecure.NewCredentials()))

client, _ := run.NewJobsClient(ctx,
    option.WithGRPCConn(conn))
```

## API Surface

### Jobs (`google.cloud.run.v2.Jobs`)

| Method | Description |
|--------|-------------|
| `CreateJob` | Register a new job |
| `GetJob` | Get job configuration |
| `ListJobs` | List all registered jobs |
| `DeleteJob` | Remove a job |
| `RunJob` | Start a job execution |

### Executions (`google.cloud.run.v2.Executions`)

| Method | Description |
|--------|-------------|
| `GetExecution` | Get execution status |
| `ListExecutions` | List executions for a job |
| `DeleteExecution` | Remove an execution record |
| `CancelExecution` | Stop a running execution |

## How It Works

1. **RunJob** is called with a job name and optional environment overrides
2. The emulator looks up the job definition (from YAML config or API-created)
3. It merges the override env vars with the job's default env vars
4. It starts the container (Docker) or command (subprocess) **asynchronously**
5. It returns a `longrunning.Operation` with the execution name immediately
6. The client polls **GetExecution** to check completion status

## Debugging

gRPC reflection is enabled, so you can use [grpcurl](https://github.com/fullstorydev/grpcurl):

```bash
# List services
grpcurl -plaintext localhost:8123 list

# List jobs
grpcurl -plaintext localhost:8123 google.cloud.run.v2.Jobs/ListJobs

# Get a specific job
grpcurl -plaintext -d '{"name": "projects/fake-project/locations/us-central1/jobs/my-job"}' \
  localhost:8123 google.cloud.run.v2.Jobs/GetJob
```

## License

[BSD 2-Clause](LICENSE)
