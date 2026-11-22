package main_test

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/testbed/bed"
)

// queueWatch reads Bazarr's job queue directly, every 50 ms, for as long as
// the check runs: often enough to see every sync of the bed pending or
// running, the shortest of which lasts about half a second.
type queueWatch struct {
	mu    sync.Mutex
	reads [][]queuedJob
	err   error
	paths []string
}

// queuedJob is a job of Bazarr's queue as Bazarr answers it.
type queuedJob struct {
	ID     int    `json:"job_id"`
	Name   string `json:"job_name"`
	Status string `json:"status"`
}

func watchQueue(t *testing.T, m bed.Manifest) *queueWatch {
	q := &queueWatch{paths: paths(m.Subtitles)}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			var answer struct{ Data []queuedJob }
			err := m.Get("system/jobs", &answer)
			q.mu.Lock()
			if err != nil && q.err == nil {
				q.err = err
			}
			q.reads = append(q.reads, answer.Data)
			q.mu.Unlock()
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	return q
}

// mark returns the place from which sawActive and mostAtOnce count: the
// reads before it include one begun after mark was called, which shows
// every job queued before then.
func (q *queueWatch) mark() int {
	for n := q.count() + 2; ; time.Sleep(10 * time.Millisecond) {
		if got := q.count(); got >= n {
			return got
		}
	}
}

// count returns how many reads have completed.
func (q *queueWatch) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.reads)
}

// syncPath returns the path of a sync job of a placed subtitle (S1).
func (q *queueWatch) syncPath(j queuedJob) (string, bool) {
	for _, prefix := range []string{"Syncing ", "Synced "} {
		if p, ok := strings.CutPrefix(j.Name, prefix); ok && slices.Contains(q.paths, p) {
			return p, true
		}
	}
	return "", false
}

// sawActive reports whether a read since from showed a sync job of path
// pending or running.
func (q *queueWatch) sawActive(from int, path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, read := range q.reads[from:] {
		for _, j := range read {
			if p, ok := q.syncPath(j); ok && p == path && (j.Status == "pending" || j.Status == "running") {
				return true
			}
		}
	}
	return false
}

// mostAtOnce returns, for each run, the most sync jobs of that run that one
// read since from showed pending or running. Bazarr numbers its jobs in the
// order it queues them, and a run has one request in flight at a time (the
// technical specification's Approach), so the sync jobs that appeared since
// from are, in that order, the runs' accepted sync requests in the order
// they were sent.
func (q *queueWatch) mostAtOnce(t *testing.T, from int, runs [][]*sentRequest) []int {
	t.Helper()
	q.mark() // a read begun after every run ended sees each job queued
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		t.Fatalf("reading Bazarr's queue: %v", q.err)
	}
	before := map[int]bool{}
	for _, read := range q.reads[:from] {
		for _, j := range read {
			before[j.ID] = true
		}
	}
	jobPath, active := map[int]string{}, map[int]bool{}
	for _, read := range q.reads[from:] {
		for _, j := range read {
			if p, ok := q.syncPath(j); ok && !before[j.ID] {
				jobPath[j.ID] = p
				active[j.ID] = active[j.ID] || j.Status == "pending" || j.Status == "running"
			}
		}
	}
	ids := slices.Sorted(func(yield func(int) bool) {
		for id := range jobPath {
			if !yield(id) {
				return
			}
		}
	})
	runOf := map[int]int{}
	i := 0
	for run, syncs := range runs {
		for _, s := range syncs {
			if s.status != 204 {
				t.Fatalf("Bazarr answered a sync with %d: %s", s.status, describe([]*sentRequest{s}))
			}
			if i >= len(ids) || jobPath[ids[i]] != s.query.Get("path") {
				t.Fatalf("the queue showed sync jobs %v; they do not match the runs' syncs in order", jobPath)
			}
			runOf[ids[i]] = run
			i++
		}
	}
	if i != len(ids) {
		t.Fatalf("the queue showed %d sync jobs, the runs sent %d syncs", len(ids), i)
	}
	most := make([]int, len(runs))
	for _, id := range ids {
		if !active[id] {
			t.Errorf("no read of the queue showed the sync job of %s pending or running", jobPath[id])
		}
	}
	for _, read := range q.reads[from:] {
		count := make([]int, len(runs))
		for _, j := range read {
			if run, ok := runOf[j.ID]; ok && (j.Status == "pending" || j.Status == "running") {
				count[run]++
			}
		}
		for run, n := range count {
			most[run] = max(most[run], n)
		}
	}
	return most
}
