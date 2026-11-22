package fakebazarr

import "slices"

// JobState is where a job of Bazarr's queue is. Bazarr 1.6.2 keeps four
// queues, lists them in the order pending, running, failed, completed, and
// keeps listing a job after it ended, as seen on the test bed.
type JobState int

const (
	JobPending JobState = iota + 1
	// JobMoving: taken out of the pending queue and not yet appended to the
	// running one, so that a read shows the job in neither (S2).
	JobMoving
	JobRunning
	JobFailed
	JobCompleted
)

var jobStatus = map[JobState]string{JobPending: "pending", JobRunning: "running",
	JobFailed: "failed", JobCompleted: "completed"}

// listOrder is the order Bazarr lists its queues in.
var listOrder = []JobState{JobPending, JobRunning, JobFailed, JobCompleted}

type job struct {
	id    int
	name  string
	state JobState
	// path and video are a sync job's subtitle.
	path  string
	video videoKey
}

// videoKey is a movie ("movie", Radarr ID) or an episode ("episode",
// Sonarr episode ID).
type videoKey struct {
	typ string
	id  int
}

// queueSync queues the sync of subPath as Bazarr does, named "Syncing
// <path>" with the path as sent; every request queues a job, also one
// identical to a sync still pending or running, as seen on the test bed.
// The caller holds mu.
func (s *Server) queueSync(subPath string, video videoKey) {
	s.lastJobID++
	s.jobs = append(s.jobs, &job{id: s.lastJobID, name: "Syncing " + subPath, state: JobPending,
		path: subPath, video: video})
}

// SyncJob returns the ID of the latest job queued for a sync of subPath.
func (s *Server) SyncJob(subPath string) (id int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range slices.Backward(s.jobs) {
		if j.path == subPath {
			return j.id, true
		}
	}
	return 0, false
}

// AddJob adds a job named name, such as one of Bazarr's own tasks, in
// state, and returns its ID.
func (s *Server) AddJob(name string, state JobState) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastJobID++
	s.jobs = append(s.jobs, &job{id: s.lastJobID, name: name, state: state})
	return s.lastJobID
}

// SetJob moves the job to state.
func (s *Server) SetJob(id int, state JobState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job(id).state = state
}

// SyncRan does what Bazarr does once a sync job has run, before the job
// leaves the running queue: it renames the job "Synced <path>" and, when
// eventTime is not empty, writes a sync event (action 5) for the path at
// that `parsed_timestamp` in the job's video's history. A sync that did not
// replace the file writes none.
func (s *Server) SyncRan(id int, eventTime string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.job(id)
	j.name = "Synced " + j.path
	if eventTime != "" {
		s.addEvent(j.video, Event{Action: 5, Time: eventTime, Path: j.path})
	}
}

// Restart is a restart of Bazarr: a new start time (a JSON number) and an
// empty queue, whose job IDs start again from 1 (S3).
func (s *Server) Restart(startTime string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.StartTime = startTime
	s.jobs, s.lastJobID = nil, 0
}

// job returns the job with that ID; the caller holds mu.
func (s *Server) job(id int) *job {
	for _, j := range s.jobs {
		if j.id == id {
			return j
		}
	}
	s.t.Fatalf("fakebazarr: no job %d", id)
	return nil
}

// queue answers GET system/jobs: {"data": [...]}, the queues in Bazarr's
// order; the caller holds mu.
func (s *Server) queue() any {
	data := []map[string]any{}
	for _, state := range listOrder {
		for _, j := range s.jobs {
			if j.state != state {
				continue
			}
			data = append(data, map[string]any{
				"job_id": j.id, "job_name": j.name, "status": jobStatus[state],
				"last_run_time": "2026-11-22T09:17:01.601000+00:00", "is_progress": true, "is_signalr": false,
				"progress_value": 0, "progress_max": 1, "progress_message": "",
			})
		}
	}
	return map[string]any{"data": data}
}
