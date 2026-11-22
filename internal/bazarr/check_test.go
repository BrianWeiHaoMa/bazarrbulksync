package bazarr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/bazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakebazarr"
	"github.com/BrianWeiHaoMa/bazarrbulksync/internal/fakeclock"
)

// checkFails runs the connection check and returns its cause, failing the
// test unless the check failed with a *CheckError and returned no client.
func checkFails(t *testing.T, conn bazarr.Connection) *bazarr.CheckError {
	t.Helper()
	client, err := bazarr.Connect(context.Background(), conn)
	if client != nil {
		t.Fatalf("Connect returned a client with cause %v", err)
	}
	var ce *bazarr.CheckError
	if !errors.As(err, &ce) {
		t.Fatalf("Connect error = %#v, want *CheckError", err)
	}
	return ce
}

func TestCheckSucceeds(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Version: "1.6.2", StartTime: "1763700000.5"})
	client, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey()})
	if err != nil {
		t.Fatal(err)
	}
	if got := client.Status(); got.Version != "1.6.2" || got.StartTime != "1763700000.5" {
		t.Errorf("Status() = %+v", got)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("the check sent %d requests, want 1", n)
	}
}

func TestCheckMissing(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	ce := checkFails(t, bazarr.Connection{URL: "", APIKey: srv.APIKey()})
	if ce.Failure != bazarr.MissingAddress {
		t.Errorf("empty address: failure %v, want MissingAddress", ce.Failure)
	}
	ce = checkFails(t, bazarr.Connection{URL: srv.URL, APIKey: ""})
	if ce.Failure != bazarr.MissingAPIKey || !strings.Contains(ce.Error(), srv.URL) {
		t.Errorf("empty key: failure %v, %q; want MissingAPIKey naming %s", ce.Failure, ce.Error(), srv.URL)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("fake received %d requests, want none", n)
	}
}

func TestCheckUnusableAddress(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	host := strings.TrimPrefix(srv.URL, "http://")
	for _, address := range []string{
		host,                   // no scheme: "127.0.0.1:port"
		"localhost:6767",       // parses with scheme "localhost"
		"ftp://" + host,        // another scheme
		"http://",              // no host
		"http://[::1",          // does not parse
		"http:" + host,         // opaque
		srv.URL + "/?apikey=1", // a query the calls cannot keep
		"bazarr.local/bazarr",  // a bare path
		srv.URL + "#fragment",  // a fragment
	} {
		ce := checkFails(t, bazarr.Connection{URL: address, APIKey: srv.APIKey()})
		named, _, _ := strings.Cut(address, "?")
		named, _, _ = strings.Cut(named, "#")
		if ce.Failure != bazarr.UnusableAddress || !strings.Contains(ce.Error(), named) {
			t.Errorf("%q: failure %v, %q; want UnusableAddress naming it", address, ce.Failure, ce.Error())
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("fake received %d requests, want none", n)
	}
}

func TestCheckNoAnswer(t *testing.T) {
	t.Run("nothing listens", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := "http://" + l.Addr().String()
		l.Close()
		ce := checkFails(t, bazarr.Connection{URL: address, APIKey: "k"})
		if ce.Failure != bazarr.NoAnswer || !strings.Contains(ce.Error(), address) {
			t.Errorf("failure %v, %q; want NoAnswer naming %s", ce.Failure, ce.Error(), address)
		}
	})
	t.Run("held at the limit, one attempt", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{})
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Hold })
		clock := fakeclock.New(t)
		done := make(chan error, 1)
		go func() {
			_, err := bazarr.Connect(context.Background(), bazarr.Connection{
				URL: srv.URL, APIKey: srv.APIKey(), Clock: clock, Limits: bazarr.Limits{Read: 7e9}})
			done <- err
		}()
		srv.WaitHeld()
		if d := clock.WaitTimer(); d != 7e9 {
			t.Fatalf("request limit = %v, want the 7s the test set", d)
		}
		clock.Advance(7e9)
		err := await(t, done)
		var ce *bazarr.CheckError
		if !errors.As(err, &ce) || ce.Failure != bazarr.NoAnswer {
			t.Fatalf("err = %v, want NoAnswer", err)
		}
		if n := len(srv.Requests()); n != 1 {
			t.Errorf("the check sent %d requests, want exactly 1", n)
		}
	})
	t.Run("proxy says Bazarr did not answer", func(t *testing.T) {
		srv := fakebazarr.New(t, fakebazarr.Config{})
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Status(http.StatusBadGateway) })
		if ce := checkFails(t, bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey()}); ce.Failure != bazarr.NoAnswer {
			t.Errorf("502: failure %v, want NoAnswer", ce.Failure)
		}
	})
}

func TestCheckRejectedKey(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	ce := checkFails(t, bazarr.Connection{URL: srv.URL, APIKey: "wrong-key"})
	if ce.Failure != bazarr.RejectedKey || !strings.Contains(ce.Error(), srv.URL) {
		t.Errorf("failure %v, %q; want RejectedKey naming %s", ce.Failure, ce.Error(), srv.URL)
	}

	// A 401 whose body never arrives is still a rejected key.
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer stalled.Close()
	clock := fakeclock.New(t)
	done := make(chan error, 1)
	go func() {
		_, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: stalled.URL, APIKey: "k", Clock: clock})
		done <- err
	}()
	select {
	case err := <-done:
		var ce *bazarr.CheckError
		if !errors.As(err, &ce) || ce.Failure != bazarr.RejectedKey {
			t.Errorf("stalled 401 body: err %v, want RejectedKey", err)
		}
	case <-time.After(10 * time.Second):
		clock.Advance(time.Hour)
		t.Errorf("stalled 401 body: the check waited for the body")
	}
}

func TestCheckRedirectNotFollowed(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	redirector := httptest.NewServer(http.RedirectHandler(srv.URL+"/api/system/status", http.StatusFound))
	defer redirector.Close()
	ce := checkFails(t, bazarr.Connection{URL: redirector.URL, APIKey: srv.APIKey()})
	if ce.Failure != bazarr.NotBazarr {
		t.Errorf("failure %v, want NotBazarr", ce.Failure)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("the redirect's target received %d requests carrying the key, want none", n)
	}
}

func TestCheckVersion(t *testing.T) {
	for version, ok := range map[string]bool{
		"1.6.0": true, "1.6.2": true, "1.10.0": true, "2.0.0": true, "1.6.3-beta.5": true, "1.7.0-beta.1": true,
		"1.6.2+build.7": true,
		"1.5.9":         false, "1.6.0-beta.3": false, "": false, "unknown": false, "1.6": false,
		"0.9.9": false, "v1.6.2": false, "1.06.0": false, "1.6.x": false, "1.6.0-": false,
		"1.6.2+": false, "1.6.2-.": false, "1.6.2-rc 1": false, "1.6.0+build": true, "1.6.0-rc.1+b": false,
	} {
		srv := fakebazarr.New(t, fakebazarr.Config{})
		srv.SetVersion(version)
		client, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey()})
		if ok {
			if err != nil {
				t.Errorf("version %q: %v, want a client", version, err)
			}
			continue
		}
		var ce *bazarr.CheckError
		if client != nil || !errors.As(err, &ce) || ce.Failure != bazarr.UnsupportedVersion ||
			!strings.Contains(ce.Error(), srv.URL) {
			t.Errorf("version %q: client %v, err %v; want UnsupportedVersion naming the address", version, client, err)
		}
	}
}

func TestCheckNotBazarr(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{BasePath: "/bazarr"})
	root := strings.TrimSuffix(srv.URL, "/bazarr")
	// A wrong base path is answered by Bazarr's web page with HTTP 200.
	ce := checkFails(t, bazarr.Connection{URL: root + "/wrong", APIKey: srv.APIKey()})
	if ce.Failure != bazarr.NotBazarr {
		t.Errorf("web page: failure %v, want NotBazarr", ce.Failure)
	}
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Status(status) })
		if ce := checkFails(t, bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey()}); ce.Failure != bazarr.NotBazarr {
			t.Errorf("HTTP %d: failure %v, want NotBazarr", status, ce.Failure)
		}
	}
	srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Garble })
	if ce := checkFails(t, bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey()}); ce.Failure != bazarr.NotBazarr {
		t.Errorf("garbled: failure %v, want NotBazarr", ce.Failure)
	}
}

// TestCheckFailedGivesNoClient pins that reads exist only as methods of the
// client a successful check returns: every failed check returns nil.
func TestCheckFailedGivesNoClient(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{Version: "1.5.0"})
	client, err := bazarr.Connect(context.Background(), bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey()})
	if client != nil || err == nil {
		t.Fatalf("Connect = %v, %v; want no client and a cause", client, err)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("fake received %d requests, want only the check", n)
	}
}

func TestCheckStopped(t *testing.T) {
	srv := fakebazarr.New(t, fakebazarr.Config{})
	srv.Respond(func(fakebazarr.Request) fakebazarr.Behavior { return fakebazarr.Hold })
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := bazarr.Connect(ctx, bazarr.Connection{URL: srv.URL, APIKey: srv.APIKey(), Clock: fakeclock.New(t)})
		done <- err
	}()
	srv.WaitHeld()
	stop()
	if err := await(t, done); !errors.Is(err, bazarr.ErrStopped) {
		t.Fatalf("err = %v, want ErrStopped", err)
	}
}
