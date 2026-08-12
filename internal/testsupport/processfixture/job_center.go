package processfixture

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

// JobCenter is a hermetic process.JobCenter for host tests.
type JobCenter struct {
	mu   sync.Mutex
	Jobs map[string]process.JobInfo
}

func NewJobCenter(jobs ...process.JobInfo) *JobCenter {
	center := &JobCenter{Jobs: map[string]process.JobInfo{}}
	for _, job := range jobs {
		center.Jobs[job.ID] = job
	}
	return center
}

func (f *JobCenter) List() []process.JobInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]process.JobInfo, 0, len(f.Jobs))
	for _, job := range f.Jobs {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *JobCenter) Add(job process.JobInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Jobs[job.ID] = job
}

func (f *JobCenter) Info(id string) (process.JobInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.Jobs[id]
	return job, ok
}

func (f *JobCenter) Poll(_ context.Context, id string, _ bool) (process.JobInfo, error) {
	job, ok := f.Info(id)
	if !ok {
		return process.JobInfo{}, errors.New("job not found")
	}
	if job.Status == process.JobStatusStale {
		return process.JobInfo{}, errors.New("stale job cannot be polled")
	}
	return job, nil
}

func (f *JobCenter) Stdin(id, data string) error {
	job, ok := f.Info(id)
	if !ok {
		return errors.New("job not found")
	}
	if job.Status == process.JobStatusStale || !job.Running {
		return errors.New("job cannot accept stdin")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	job.OutputTail += data
	f.Jobs[id] = job
	return nil
}

func (f *JobCenter) Cancel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Jobs[id]; !ok {
		return errors.New("job not found")
	}
	delete(f.Jobs, id)
	return nil
}

func (f *JobCenter) CancelAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Jobs = map[string]process.JobInfo{}
}
