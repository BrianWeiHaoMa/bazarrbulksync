package main_test

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// The network cut between a run and Bazarr, made in changeBed's proxy while
// Bazarr keeps running: from startCut to endCut no request of the run
// reaches Bazarr, and no answer of Bazarr reaches the run, the answer to a
// request sent before the cut included. A request held by the cut gets no
// answer at all: the proxy drops its connection once the run gives up on it
// or the cut ends.

// errCut is how answerUnlessCut tells the proxy to drop an answer.
var errCut = errors.New("the network is cut")

// sentKey carries a request's sentRequest from the proxy's handler to its
// answer.
type sentKey struct{}

// startCut cuts the network and returns when the cut began.
func (b *changeBed) startCut() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cut = make(chan struct{})
	return time.Now()
}

// endCut ends the cut and returns when it ended.
func (b *changeBed) endCut() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.cut)
	b.cut = nil
	return time.Now()
}

// holdIfCut, for a request arriving while the network is cut, waits until
// the run gives up on it or the cut ends, then drops its connection, so
// that it never reaches Bazarr. Otherwise it returns the request, carrying
// req for answerUnlessCut.
func (b *changeBed) holdIfCut(r *http.Request, req *sentRequest) *http.Request {
	b.mu.Lock()
	cut := b.cut
	b.mu.Unlock()
	if cut != nil {
		held(r.Context(), cut)
		panic(http.ErrAbortHandler)
	}
	return r.WithContext(context.WithValue(r.Context(), sentKey{}, req))
}

// answerUnlessCut lets Bazarr's answer through, noting when, unless the
// network is cut; then it holds the answer as holdIfCut holds a request.
func (b *changeBed) answerUnlessCut(res *http.Response) error {
	req := res.Request.Context().Value(sentKey{}).(*sentRequest)
	b.mu.Lock()
	cut := b.cut
	if cut == nil {
		req.answered = time.Now()
	}
	b.mu.Unlock()
	if cut != nil {
		held(res.Request.Context(), cut)
		return errCut
	}
	return nil
}

// held waits until ctx ends or cut closes.
func held(ctx context.Context, cut chan struct{}) {
	select {
	case <-ctx.Done():
	case <-cut:
	}
}

// answerError drops the connection of an answer the cut held, and of a
// request Bazarr did not answer while the network is cut. Otherwise Bazarr
// did not answer, as while it restarts, and it answers as a proxy does then.
func (b *changeBed) answerError(w http.ResponseWriter, r *http.Request, err error) {
	b.mu.Lock()
	cut := b.cut
	b.mu.Unlock()
	if cut != nil {
		held(r.Context(), cut)
	}
	if errors.Is(err, errCut) || cut != nil {
		panic(http.ErrAbortHandler)
	}
	w.WriteHeader(http.StatusBadGateway)
}
