package bazarr

import (
	"context"
	"time"
)

// Job is a job in Bazarr's queue, as Bazarr names it.
type Job struct {
	Name string
	// Status is Bazarr's: pending, running, failed, or completed. Bazarr
	// keeps listing a job after it ended.
	Status string
}

// Queue is what one read of Bazarr's job queue showed, for every path at
// once.
type Queue struct {
	Jobs []Job
	// Sent is when the request that was answered was sent, and Answered when
	// its answer was complete, on the client's clock; after a retry, those of
	// the attempt that succeeded. The rules that compare queue reads five
	// seconds apart compare these.
	Sent, Answered time.Time
}

// HasSyncOf reports whether the queue shows a sync job of the subtitle at
// path: a job whose status is pending or running and whose name is exactly
// "Syncing <path>" or "Synced <path>" (S1). A job Bazarr still lists after
// it ended is none, and neither is a job for any other path.
func (q Queue) HasSyncOf(path string) bool {
	for _, j := range q.Jobs {
		if (j.Status == "pending" || j.Status == "running") &&
			(j.Name == "Syncing "+path || j.Name == "Synced "+path) {
			return true
		}
	}
	return false
}

type queueAnswer struct {
	Data *[]struct {
		JobName *string `json:"job_name"`
		Status  *string `json:"status"`
	} `json:"data"`
}

// complete rejects what is not Bazarr's queue, such as a body without its
// members, so that it is never taken for an empty queue.
func (a *queueAnswer) complete() bool {
	if a.Data == nil {
		return false
	}
	for _, j := range *a.Data {
		if j.JobName == nil || j.Status == nil {
			return false
		}
	}
	return true
}

// Queue reads Bazarr's job queue, a read under G3.
func (c *Client) Queue(ctx context.Context) (Queue, Retried, error) {
	var answer *queueAnswer
	at, retried, err := c.timedRead(ctx, "system/jobs", nil, func() any { answer = &queueAnswer{}; return answer })
	if err != nil {
		return Queue{}, retried, err
	}
	q := Queue{Jobs: make([]Job, 0, len(*answer.Data)), Sent: at.sent, Answered: at.answered}
	for _, j := range *answer.Data {
		q.Jobs = append(q.Jobs, Job{Name: *j.JobName, Status: *j.Status})
	}
	return q, retried, nil
}
