package state

// Job represents a registered Cloud Run job.
type Job struct {
	// Full resource name: projects/{project}/locations/{location}/jobs/{job}
	Name    string
	Image   string
	Command []string
	Env     map[string]string
}

// ShortName extracts the job ID from the full resource name.
func (j *Job) ShortName() string {
	return parseLastSegment(j.Name)
}
