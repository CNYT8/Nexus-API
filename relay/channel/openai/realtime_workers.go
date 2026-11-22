package openai

import (
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

// Each reader owns one socket direction. Any exit cancels the pair; the closer
// interrupts blocked reads/writes and the caller joins both before settlement.
// The bounded error channel is diagnostic only, never a completion barrier.
func runRealtimeWorkers(ctx context.Context, closeConnections func(), client, target func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		<-ctx.Done()
		closeConnections()
	}()
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	start := func(name string, run func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer cancel()
			var err error
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic in %s reader: %v", name, r)
				}
				if err != nil {
					select {
					case errors <- err:
					default:
					}
				}
			}()
			err = run(ctx)
		}()
	}
	start("client", client)
	start("target", target)
	workers.Wait()
	<-closed
	select {
	case err := <-errors:
		return err
	default:
		return nil
	}
}

// Keep accounting serialized, but release it before writing to the other peer:
// backpressure must not hold the lock needed to finish an in-flight response.
func relayRealtimeMessages(ctx context.Context, source, destination *websocket.Conn, stateMu *sync.Mutex, handle func([]byte) error) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, message, err := source.ReadMessage()
		if err != nil {
			if ctx.Err() != nil || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("error reading realtime message: %w", err)
		}
		err = func() error {
			stateMu.Lock()
			defer stateMu.Unlock()
			return handle(message)
		}()
		if err != nil {
			return err
		}
		if err := destination.WriteMessage(websocket.TextMessage, message); err != nil {
			return fmt.Errorf("error writing realtime message: %w", err)
		}
	}
}
