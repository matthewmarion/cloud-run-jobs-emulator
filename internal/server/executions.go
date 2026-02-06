package server

import (
	"context"
	"log/slog"
	"time"

	runpb "cloud.google.com/go/run/apiv2/runpb"
	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/executor"
	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/state"
	longrunningpb "google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

type ExecutionsServer struct {
	runpb.UnimplementedExecutionsServer
	store    *state.Store
	executor executor.Executor
}

func (s *ExecutionsServer) GetExecution(ctx context.Context, req *runpb.GetExecutionRequest) (*runpb.Execution, error) {
	slog.Info("GetExecution called", "name", req.Name)

	exec, err := s.store.GetExecution(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "execution not found: %s", req.Name)
	}

	return executionToProto(exec), nil
}

func (s *ExecutionsServer) ListExecutions(ctx context.Context, req *runpb.ListExecutionsRequest) (*runpb.ListExecutionsResponse, error) {
	slog.Info("ListExecutions called", "parent", req.Parent)

	execs := s.store.ListExecutions(req.Parent)
	var pbExecs []*runpb.Execution
	for _, e := range execs {
		pbExecs = append(pbExecs, executionToProto(e))
	}

	return &runpb.ListExecutionsResponse{
		Executions: pbExecs,
	}, nil
}

func (s *ExecutionsServer) DeleteExecution(ctx context.Context, req *runpb.DeleteExecutionRequest) (*longrunningpb.Operation, error) {
	slog.Info("DeleteExecution called", "name", req.Name)

	exec, err := s.store.GetExecution(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "execution not found: %s", req.Name)
	}

	execProto := executionToProto(exec)
	if err := s.store.DeleteExecution(req.Name); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete execution: %v", err)
	}

	respAny, err := anypb.New(execProto)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal response: %v", err)
	}

	return &longrunningpb.Operation{
		Name:   req.Name,
		Done:   true,
		Result: &longrunningpb.Operation_Response{Response: respAny},
	}, nil
}

func (s *ExecutionsServer) CancelExecution(ctx context.Context, req *runpb.CancelExecutionRequest) (*longrunningpb.Operation, error) {
	slog.Info("CancelExecution called", "name", req.Name)

	exec, err := s.store.GetExecution(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "execution not found: %s", req.Name)
	}

	if exec.Status != state.StatusRunning {
		return nil, status.Errorf(codes.FailedPrecondition, "execution is not running: %s", exec.Status)
	}

	if err := s.executor.Cancel(exec); err != nil {
		slog.Warn("failed to cancel execution", "error", err)
	}

	exec.Status = state.StatusCancelled
	exec.CompletionTime = time.Now()

	execProto := executionToProto(exec)
	respAny, err := anypb.New(execProto)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal response: %v", err)
	}

	return &longrunningpb.Operation{
		Name:   req.Name,
		Done:   true,
		Result: &longrunningpb.Operation_Response{Response: respAny},
	}, nil
}
