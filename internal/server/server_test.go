package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	runpb "cloud.google.com/go/run/apiv2/runpb"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/executor"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/server"
	"github.com/matthewmarion/cloud-run-jobs-emulator/internal/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func startTestServer(t *testing.T, store *state.Store) (string, func()) {
	t.Helper()

	exec := executor.NewSubprocessExecutor()
	srv := server.New(store, exec, "test-project", "us-central1")

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	go srv.Serve(lis)

	addr := lis.Addr().String()

	return addr, func() {
		srv.Stop()
	}
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestCreateAndGetJob(t *testing.T) {
	store := state.NewStore()
	addr, cleanup := startTestServer(t, store)
	defer cleanup()

	conn := dial(t, addr)
	defer conn.Close()

	client := runpb.NewJobsClient(conn)
	ctx := context.Background()

	// Create a job
	createResp, err := client.CreateJob(ctx, &runpb.CreateJobRequest{
		Parent: "projects/test-project/locations/us-central1",
		JobId:  "test-job",
		Job: &runpb.Job{
			Template: &runpb.ExecutionTemplate{
				TaskCount:   1,
				Parallelism: 1,
				Template: &runpb.TaskTemplate{
					Containers: []*runpb.Container{
						{
							Image:   "alpine:latest",
							Command: []string{"echo", "hello"},
							Env: []*runpb.EnvVar{
								{Name: "FOO", Values: &runpb.EnvVar_Value{Value: "bar"}},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	if !createResp.Done {
		t.Error("expected operation to be done")
	}

	// Get the job
	job, err := client.GetJob(ctx, &runpb.GetJobRequest{
		Name: "projects/test-project/locations/us-central1/jobs/test-job",
	})
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if job.Name != "projects/test-project/locations/us-central1/jobs/test-job" {
		t.Errorf("unexpected job name: %s", job.Name)
	}
}

func TestListJobs(t *testing.T) {
	store := state.NewStore()
	store.SaveJob(&state.Job{
		Name:  "projects/test-project/locations/us-central1/jobs/job1",
		Image: "alpine:latest",
		Env:   map[string]string{},
	})
	store.SaveJob(&state.Job{
		Name:  "projects/test-project/locations/us-central1/jobs/job2",
		Image: "alpine:latest",
		Env:   map[string]string{},
	})

	addr, cleanup := startTestServer(t, store)
	defer cleanup()

	conn := dial(t, addr)
	defer conn.Close()

	client := runpb.NewJobsClient(conn)
	ctx := context.Background()

	resp, err := client.ListJobs(ctx, &runpb.ListJobsRequest{
		Parent: "projects/test-project/locations/us-central1",
	})
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(resp.Jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(resp.Jobs))
	}
}

func TestRunJobAndGetExecution(t *testing.T) {
	store := state.NewStore()
	store.SaveJob(&state.Job{
		Name:    "projects/test-project/locations/us-central1/jobs/echo-job",
		Image:   "alpine:latest",
		Command: []string{"echo", "hello"},
		Env:     map[string]string{"DEFAULT_VAR": "default_value"},
	})

	addr, cleanup := startTestServer(t, store)
	defer cleanup()

	conn := dial(t, addr)
	defer conn.Close()

	jobsClient := runpb.NewJobsClient(conn)
	execClient := runpb.NewExecutionsClient(conn)
	ctx := context.Background()

	// Run job with overrides
	op, err := jobsClient.RunJob(ctx, &runpb.RunJobRequest{
		Name: "projects/test-project/locations/us-central1/jobs/echo-job",
		Overrides: &runpb.RunJobRequest_Overrides{
			ContainerOverrides: []*runpb.RunJobRequest_Overrides_ContainerOverride{
				{
					Env: []*runpb.EnvVar{
						{Name: "OVERRIDE_VAR", Values: &runpb.EnvVar_Value{Value: "override_value"}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}
	if op.Done {
		t.Error("expected operation to not be done immediately")
	}

	executionName := op.Name

	// Wait for execution to complete (subprocess executor runs echo quickly)
	time.Sleep(500 * time.Millisecond)

	// Get execution status
	exec, err := execClient.GetExecution(ctx, &runpb.GetExecutionRequest{
		Name: executionName,
	})
	if err != nil {
		t.Fatalf("GetExecution failed: %v", err)
	}

	if exec.SucceededCount != 1 {
		t.Errorf("expected succeeded count 1, got %d", exec.SucceededCount)
	}
}

func TestRunJobNotFound(t *testing.T) {
	store := state.NewStore()
	addr, cleanup := startTestServer(t, store)
	defer cleanup()

	conn := dial(t, addr)
	defer conn.Close()

	client := runpb.NewJobsClient(conn)
	ctx := context.Background()

	_, err := client.RunJob(ctx, &runpb.RunJobRequest{
		Name: "projects/test-project/locations/us-central1/jobs/nonexistent",
	})
	if err == nil {
		t.Error("expected error for nonexistent job")
	}
}

func TestDeleteJob(t *testing.T) {
	store := state.NewStore()
	store.SaveJob(&state.Job{
		Name:  "projects/test-project/locations/us-central1/jobs/to-delete",
		Image: "alpine:latest",
		Env:   map[string]string{},
	})

	addr, cleanup := startTestServer(t, store)
	defer cleanup()

	conn := dial(t, addr)
	defer conn.Close()

	client := runpb.NewJobsClient(conn)
	ctx := context.Background()

	_, err := client.DeleteJob(ctx, &runpb.DeleteJobRequest{
		Name: "projects/test-project/locations/us-central1/jobs/to-delete",
	})
	if err != nil {
		t.Fatalf("DeleteJob failed: %v", err)
	}

	// Verify it's gone
	_, err = client.GetJob(ctx, &runpb.GetJobRequest{
		Name: "projects/test-project/locations/us-central1/jobs/to-delete",
	})
	if err == nil {
		t.Error("expected error for deleted job")
	}
}
