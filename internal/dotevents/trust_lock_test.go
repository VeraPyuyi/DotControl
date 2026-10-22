package dotevents

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedRefreshLockWaitCannotRetainExpiredEpoch(t *testing.T) {
	for _, point := range []string{"mutation", "final"} {
		t.Run(point, func(t *testing.T) {
			base := time.Now().UTC()
			m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
			defer stop()
			p := testParams("alice")
			p.TTL = []byte("60000")
			first, err := m.subscribe(context.Background(), "alice", p, func() time.Time { return base })
			if err != nil {
				t.Fatal(err)
			}
			holder, err := openStore(dir, testKey)
			if err != nil {
				t.Fatal(err)
			}
			var seconds atomic.Int64
			seconds.Store(59)
			var lockCalls atomic.Int32
			reached := make(chan struct{})
			releaseAttempt := make(chan struct{})
			target := int32(1)
			if point == "final" {
				target = 2
			}
			m.store.beforeLock = func() {
				if lockCalls.Add(1) == target {
					close(reached)
					<-releaseAttempt
				}
			}
			done := make(chan struct{})
			var renewed Subscription
			var subscribeErr error
			go func() {
				defer close(done)
				renewed, subscribeErr = m.subscribe(context.Background(), "alice", p, func() time.Time { return base.Add(time.Duration(seconds.Load()) * time.Second) })
			}()
			<-reached
			// Hold the real store lock while the renewal has reached lock acquisition.
			err = holder.withLock(false, false, func(*persistenceState) error { seconds.Store(61); close(releaseAttempt); return nil })
			if err != nil {
				t.Fatal(err)
			}
			<-done
			if subscribeErr != nil {
				t.Fatal(subscribeErr)
			}
			if renewed.TrustEpoch == first.TrustEpoch {
				t.Fatal("cached renewal retained trust across lock-wait lease gap")
			}
			if point == "mutation" && !renewed.ExpiresAt.Equal(base.Add(121*time.Second)) {
				t.Fatalf("grant used pre-lock time: %s", renewed.ExpiresAt)
			}
		})
	}
}
