package server

import (
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	runpb "cloud.google.com/go/run/apiv2/runpb"
	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/executor"
	"github.com/mattkinnersley/cloud-run-jobs-emulator/internal/state"
)

type Server struct {
	grpcServer *grpc.Server
	store      *state.Store
	executor   executor.Executor
	projectID  string
	region     string
}

func New(store *state.Store, exec executor.Executor, projectID, region string) *Server {
	s := &Server{
		store:     store,
		executor:  exec,
		projectID: projectID,
		region:    region,
	}

	gs := grpc.NewServer()

	jobsSvc := &JobsServer{
		store:     store,
		executor:  exec,
		projectID: projectID,
		region:    region,
	}
	runpb.RegisterJobsServer(gs, jobsSvc)

	execSvc := &ExecutionsServer{
		store:    store,
		executor: exec,
	}
	runpb.RegisterExecutionsServer(gs, execSvc)

	// Enable gRPC reflection for grpcurl and debugging
	reflection.Register(gs)

	s.grpcServer = gs
	return s
}

func (s *Server) Start(port string) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", port, err)
	}

	slog.Info("starting gRPC server", "port", port)
	return s.grpcServer.Serve(lis)
}

// Serve starts the server on an existing listener.
func (s *Server) Serve(lis net.Listener) error {
	return s.grpcServer.Serve(lis)
}

func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}
