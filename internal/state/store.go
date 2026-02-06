package state

import (
	"fmt"
	"strings"
	"sync"
)

// Store is a thread-safe in-memory store for jobs and executions.
type Store struct {
	mu         sync.RWMutex
	jobs       map[string]*Job       // keyed by full resource name
	executions map[string]*Execution // keyed by full resource name
}

func NewStore() *Store {
	return &Store{
		jobs:       make(map[string]*Job),
		executions: make(map[string]*Execution),
	}
}

// SaveJob stores a job definition.
func (s *Store) SaveJob(job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.Name] = job
}

// GetJob retrieves a job by full resource name.
func (s *Store) GetJob(name string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[name]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", name)
	}
	return job, nil
}

// GetJobByShortName looks up a job by its short name across all projects/locations.
func (s *Store) GetJobByShortName(shortName string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, job := range s.jobs {
		if job.ShortName() == shortName {
			return job, nil
		}
	}
	return nil, fmt.Errorf("job not found: %s", shortName)
}

// DeleteJob removes a job by full resource name.
func (s *Store) DeleteJob(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[name]; !ok {
		return fmt.Errorf("job not found: %s", name)
	}
	delete(s.jobs, name)
	return nil
}

// ListJobs returns all jobs, optionally filtered by parent prefix.
func (s *Store) ListJobs(parent string) []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var jobs []*Job
	for _, job := range s.jobs {
		if parent == "" || strings.HasPrefix(job.Name, parent) {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// SaveExecution stores an execution record.
func (s *Store) SaveExecution(exec *Execution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions[exec.Name] = exec
}

// GetExecution retrieves an execution by full resource name.
func (s *Store) GetExecution(name string) (*Execution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	exec, ok := s.executions[name]
	if !ok {
		return nil, fmt.Errorf("execution not found: %s", name)
	}
	return exec, nil
}

// DeleteExecution removes an execution by full resource name.
func (s *Store) DeleteExecution(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.executions[name]; !ok {
		return fmt.Errorf("execution not found: %s", name)
	}
	delete(s.executions, name)
	return nil
}

// ListExecutions returns executions for a given job (by job resource name prefix).
func (s *Store) ListExecutions(jobName string) []*Execution {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var execs []*Execution
	for _, exec := range s.executions {
		if strings.HasPrefix(exec.Name, jobName+"/executions/") {
			execs = append(execs, exec)
		}
	}
	return execs
}

// parseLastSegment extracts the last path segment from a resource name.
func parseLastSegment(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) == 0 {
		return name
	}
	return parts[len(parts)-1]
}
