package common

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestDrainWaitsForSessionAndSettlement(t *testing.T) {
	drain := NewRequestDrain()
	stopping, sessionDone, ok := drain.Register()
	require.True(t, ok)
	defer sessionDone()
	_, settlementDone, ok := drain.Register()
	require.True(t, ok)
	defer settlementDone()

	drain.BeginDrain()
	drain.BeginDrain()
	select {
	case <-stopping:
	default:
		t.Fatal("registered session did not receive shutdown notification")
	}
	rejected, rejectedDone, ok := drain.Register()
	require.False(t, ok, "shutdown must stop admission before waiting for workers")
	rejectedDone()
	select {
	case <-rejected:
	default:
		t.Fatal("rejected registration did not receive the closed shutdown channel")
	}

	// A closed client is not evidence that its asynchronous billing is done.
	sessionDone()
	sessionDone()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, drain.Wait(ctx), context.DeadlineExceeded)
	settlementDone()
	settlementDone()
	require.NoError(t, drain.Wait(context.Background()))
}

func TestRequestDrainCancellationKeepsRegistration(t *testing.T) {
	drain := NewRequestDrain()
	_, done, ok := drain.Register()
	require.True(t, ok)
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, drain.Wait(ctx), context.Canceled)
	// A canceled waiter must not clear the pending count or manufacture success.
	require.ErrorIs(t, drain.Wait(ctx), context.Canceled)
	done()
	require.NoError(t, drain.Wait(ctx))
}

func TestRequestDrainZeroValueImplicitlyStartsDraining(t *testing.T) {
	var drain RequestDrain
	_, done, ok := drain.Register()
	require.True(t, ok)
	done()
	require.NoError(t, drain.Wait(context.Background()))
	_, rejectedDone, ok := drain.Register()
	require.False(t, ok)
	rejectedDone()
	drain.BeginDrain()
}

func TestRequestDrainWatcherCanCompleteAndCheckAdmission(t *testing.T) {
	drain := NewRequestDrain()
	stopping, done, ok := drain.Register()
	require.True(t, ok)
	defer done()
	watcherResult := make(chan bool, 1)
	go func() {
		<-stopping
		_, rejectedDone, admitted := drain.Register()
		rejectedDone()
		done()
		watcherResult <- admitted
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, drain.Wait(ctx), "shutdown watcher must not deadlock with the registry lock")
	select {
	case admitted := <-watcherResult:
		require.False(t, admitted)
	case <-ctx.Done():
		t.Fatal("shutdown watcher could not finish")
	}
}

func TestRequestDrainConcurrentRegistrationAndCompletion(t *testing.T) {
	drain := NewRequestDrain()
	_, initialDone, ok := drain.Register()
	require.True(t, ok)
	defer initialDone()
	start := make(chan struct{})
	var workers sync.WaitGroup
	var badNotifications atomic.Int32
	for i := 0; i < 64; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			stopping, done, admitted := drain.Register()
			if admitted {
				<-stopping
			} else {
				select {
				case <-stopping:
				default:
					badNotifications.Add(1)
				}
			}
			done()
			done()
		}()
	}
	close(start)
	drain.BeginDrain()
	initialDone()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, drain.Wait(ctx))
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-ctx.Done():
		t.Fatal("concurrent registration/cleanup did not complete")
	}
	require.Zero(t, badNotifications.Load())
}
