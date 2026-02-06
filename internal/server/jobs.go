package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	runpb "cloud.google.com/go/run/apiv2/runpb"
	"github.com/google/uuid"
	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/executor"
	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/state"
	longrunningpb "google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type JobsServer struct {
	runpb.UnimplementedJobsServer
	store     *state.Store
	executor  executor.Executor
	projectID string
	region    string
}

func (s *JobsServer) RunJob(ctx context.Context, req *runpb.RunJobRequest) (*longrunningpb.Operation, error) {
	slog.Info("RunJob called", "name", req.Name)

	job, err := s.store.GetJob(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "job not found: %s", req.Name)
	}

	executionID := uuid.New().String()[:8]
	exec := &state.Execution{
		Name:      fmt.Sprintf("%s/executions/%s", req.Name, executionID),
		Job:       job,
		Status:    state.StatusRunning,
		StartTime: time.Now(),
	}

	// Merge environment: start with job defaults, then apply overrides
	env := make(map[string]string)
	for k, v := range job.Env {
		env[k] = v
	}
	if req.Overrides != nil {
		for _, co := range req.Overrides.ContainerOverrides {
			for _, ev := range co.Env {
				env[ev.Name] = ev.GetValue()
			}
		}
	}

	s.store.SaveExecution(exec)

	// Run asynchronously
	go s.executor.Run(exec, env)

	slog.Info("execution started", "execution", exec.Name)

	// Build the Execution proto for the operation metadata
	execProto := executionToProto(exec)
	metaAny, err := anypb.New(execProto)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal metadata: %v", err)
	}

	return &longrunningpb.Operation{
		Name:     exec.Name,
		Metadata: metaAny,
		Done:     false,
	}, nil
}

func (s *JobsServer) GetJob(ctx context.Context, req *runpb.GetJobRequest) (*runpb.Job, error) {
	slog.Info("GetJob called", "name", req.Name)

	job, err := s.store.GetJob(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "job not found: %s", req.Name)
	}

	return jobToProto(job), nil
}

func (s *JobsServer) CreateJob(ctx context.Context, req *runpb.CreateJobRequest) (*longrunningpb.Operation, error) {
	slog.Info("CreateJob called", "parent", req.Parent, "job_id", req.JobId)

	name := fmt.Sprintf("%s/jobs/%s", req.Parent, req.JobId)

	// Check if job already exists
	if _, err := s.store.GetJob(name); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "job already exists: %s", name)
	}

	job := protoToJob(name, req.Job)
	s.store.SaveJob(job)

	jobProto := jobToProto(job)
	respAny, err := anypb.New(jobProto)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal response: %v", err)
	}

	return &longrunningpb.Operation{
		Name:     name,
		Done:     true,
		Result:   &longrunningpb.Operation_Response{Response: respAny},
	}, nil
}

func (s *JobsServer) DeleteJob(ctx context.Context, req *runpb.DeleteJobRequest) (*longrunningpb.Operation, error) {
	slog.Info("DeleteJob called", "name", req.Name)

	job, err := s.store.GetJob(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "job not found: %s", req.Name)
	}

	jobProto := jobToProto(job)
	if err := s.store.DeleteJob(req.Name); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete job: %v", err)
	}

	respAny, err := anypb.New(jobProto)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal response: %v", err)
	}

	return &longrunningpb.Operation{
		Name:   req.Name,
		Done:   true,
		Result: &longrunningpb.Operation_Response{Response: respAny},
	}, nil
}

func (s *JobsServer) ListJobs(ctx context.Context, req *runpb.ListJobsRequest) (*runpb.ListJobsResponse, error) {
	slog.Info("ListJobs called", "parent", req.Parent)

	jobs := s.store.ListJobs(req.Parent)
	var pbJobs []*runpb.Job
	for _, j := range jobs {
		pbJobs = append(pbJobs, jobToProto(j))
	}

	return &runpb.ListJobsResponse{
		Jobs: pbJobs,
	}, nil
}

// jobToProto converts an internal Job to its protobuf representation.
func jobToProto(j *state.Job) *runpb.Job {
	var envVars []*runpb.EnvVar
	for k, v := range j.Env {
		envVars = append(envVars, &runpb.EnvVar{
			Name:   k,
			Values: &runpb.EnvVar_Value{Value: v},
		})
	}

	return &runpb.Job{
		Name: j.Name,
		Template: &runpb.ExecutionTemplate{
			TaskCount:   1,
			Parallelism: 1,
			Template: &runpb.TaskTemplate{
				Containers: []*runpb.Container{
					{
						Image:   j.Image,
						Command: j.Command,
						Env:     envVars,
					},
				},
			},
		},
		CreateTime: timestamppb.Now(),
	}
}

// protoToJob converts a protobuf Job to the internal representation.
func protoToJob(name string, pb *runpb.Job) *state.Job {
	job := &state.Job{
		Name: name,
		Env:  make(map[string]string),
	}

	if pb.Template != nil && pb.Template.Template != nil && len(pb.Template.Template.Containers) > 0 {
		c := pb.Template.Template.Containers[0]
		job.Image = c.Image
		job.Command = c.Command
		for _, ev := range c.Env {
			if v := ev.GetValue(); v != "" {
				job.Env[ev.Name] = v
			}
		}
	}

	return job
}

// executionToProto converts an internal Execution to its protobuf representation.
func executionToProto(e *state.Execution) *runpb.Execution {
	exec := &runpb.Execution{
		Name:           e.Name,
		Job:            e.Job.Name,
		Reconciling:    e.Status == state.StatusRunning,
		SucceededCount: e.SucceededCount,
		FailedCount:    e.FailedCount,
		StartTime:      timestamppb.New(e.StartTime),
		TaskCount:      1,
		Parallelism:    1,
	}
	if !e.CompletionTime.IsZero() {
		exec.CompletionTime = timestamppb.New(e.CompletionTime)
	}

	// Map internal status to condition
	switch e.Status {
	case state.StatusRunning:
		exec.RunningCount = 1
	case state.StatusSucceeded:
		exec.Conditions = []*runpb.Condition{
			{
				Type:  "Completed",
				State: runpb.Condition_CONDITION_SUCCEEDED,
			},
		}
	case state.StatusFailed:
		exec.Conditions = []*runpb.Condition{
			{
				Type:  "Completed",
				State: runpb.Condition_CONDITION_FAILED,
			},
		}
	case state.StatusCancelled:
		exec.CancelledCount = 1
		exec.Conditions = []*runpb.Condition{
			{
				Type:  "Completed",
				State: runpb.Condition_CONDITION_FAILED,
			},
		}
	}

	return exec
}

// parseJobName extracts the short job name from a full resource name.
func parseJobName(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) >= 6 {
		return parts[5]
	}
	return fullName
}
