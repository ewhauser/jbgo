package server

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ewhauser/gbash"
)

func TestSessionTTLExpiryWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rt, err := gbash.New()
		if err != nil {
			t.Fatalf("gbash.New() error = %v", err)
		}

		srv := &serverState{
			ctx:       t.Context(),
			cfg:       normalizeConfig(Config{Runtime: rt, Name: "gbash", Version: "test", SessionTTL: 250 * time.Millisecond}),
			transport: "unix",
			sessions:  make(map[string]*serverSession),
			conns:     make(map[string]*clientConn),
		}

		done := make(chan struct{})
		defer close(done)
		go srv.reapLoop(done)

		session, err := srv.createSession()
		if err != nil {
			t.Fatalf("createSession() error = %v", err)
		}
		if got := len(srv.listSessions()); got != 1 {
			t.Fatalf("listSessions() len = %d, want 1", got)
		}

		time.Sleep(249 * time.Millisecond)
		synctest.Wait()
		if got := len(srv.listSessions()); got != 1 {
			t.Fatalf("listSessions() len = %d before TTL, want 1", got)
		}

		time.Sleep(1 * time.Millisecond)
		synctest.Wait()
		if got := len(srv.listSessions()); got != 0 {
			t.Fatalf("listSessions() len = %d after TTL, want 0", got)
		}

		_, err = srv.lookupSession(session.id)
		if !errors.Is(err, errSessionNotFound) {
			t.Fatalf("lookupSession(%q) error = %v, want errSessionNotFound", session.id, err)
		}
	})
}
