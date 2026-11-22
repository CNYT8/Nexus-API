// Package jsplugin hosts the upstream task plugin ESM contract on moejs.
// Adapted from new-api/pkg/jsplugin; JavaScript is compiled by moejs, never
// interpreted by a host-side substitute. No filesystem or module linker exists.
package jsplugin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/QuantumNous/new-api/common"
)

const (
	DefaultCallTimeout = 5 * time.Second
	DefaultConcurrency = 8
	MaxSourceBytes     = 1 << 20
	MaxResultBytes     = 1 << 20
)

var ErrCallAdmissionTimeout = errors.New("plugin call admission timed out")
var forbiddenSyntax = regexp.MustCompile(`(?m)(^|[^A-Za-z0-9_$])(async|await|import)([^A-Za-z0-9_$]|$)`)

type Options struct {
	Key         string
	Version     string
	Timeout     time.Duration
	Concurrency int
	Now         func() time.Time
	Log         func(string)
}

type HookError struct {
	Hook    string
	Message string
	wrapped error
}

func (e *HookError) Error() string { return e.wrapped.Error() }
func (e *HookError) Unwrap() error { return e.wrapped }

type Engine struct {
	key       string
	version   string
	timeout   time.Duration
	now       func() time.Time
	log       func(string)
	module    *moejs.Module
	pool      chan *runtimeInstance
	semaphore chan struct{}
	parked    chan struct{}
	hooksMu   sync.RWMutex
	hooks     map[string]moejs.Hook
	exports   map[string]struct{}
}

type runtimeInstance struct {
	runtime *moejs.Runtime
	call    *runtimeCall
}

type RawJSON []byte

func Compile(source string, options Options) (*Engine, error) {
	if len(source) == 0 || len(source) > MaxSourceBytes {
		return nil, fmt.Errorf("plugin source must contain 1..%d bytes", MaxSourceBytes)
	}
	if match := forbiddenSyntax.FindString(sourceWithoutCommentsAndStrings(source)); match != "" {
		return nil, fmt.Errorf("unsupported plugin syntax %q: plugins must be synchronous and cannot import modules", strings.TrimSpace(match))
	}
	module, err := moejs.Compile(options.Key+".js", source)
	if err != nil {
		return nil, fmt.Errorf("compile plugin: %w", err)
	}
	if requests := module.Requests(); len(requests) > 0 {
		return nil, fmt.Errorf("unsupported plugin syntax: re-export from %q", requests[0])
	}
	if options.Timeout <= 0 {
		options.Timeout = DefaultCallTimeout
	}
	if options.Concurrency <= 0 {
		options.Concurrency = DefaultConcurrency
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	e := &Engine{key: options.Key, version: options.Version, timeout: options.Timeout,
		now: options.Now, log: options.Log, module: module,
		pool:      make(chan *runtimeInstance, options.Concurrency),
		semaphore: make(chan struct{}, options.Concurrency), parked: make(chan struct{}, options.Concurrency),
		hooks: make(map[string]moejs.Hook), exports: make(map[string]struct{})}
	instance, err := e.newRuntime(context.Background())
	if err != nil {
		return nil, err
	}
	for _, name := range module.Exports() {
		if value, found := instance.runtime.Export(name); found && !value.IsUndefined() {
			e.exports[name] = struct{}{}
		}
	}
	e.putRuntime(instance)
	return e, nil
}

func (e *Engine) HasExport(name string) bool { _, ok := e.exports[name]; return ok }

func (e *Engine) acquire(ctx context.Context) error {
	// Bound admission too: a cancelled/overloaded tenant cannot queue forever.
	timer := time.NewTimer(e.timeout)
	defer timer.Stop()
	select {
	case e.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrCallAdmissionTimeout
	}
}

func (e *Engine) Export(ctx context.Context, name string) (any, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.semaphore }()
	instance, err := e.getRuntime(ctx)
	if err != nil {
		return nil, err
	}
	watch := watchRuntimeContext(instance.runtime, ctx, e.timeout, errors.New("plugin export timed out"))
	reusable := false
	defer func() {
		watch.stop()
		instance.runtime.ClearInterrupt()
		if reusable {
			instance.runtime.ReleaseCallData()
			e.putRuntime(instance)
		}
	}()
	value, found := instance.runtime.Export(name)
	if !found || value.IsUndefined() {
		return nil, fmt.Errorf("plugin export %q not found", name)
	}
	result, err := instance.runtime.ToGo(value)
	reusable = err == nil
	return result, err
}

func (e *Engine) HasCallablePath(ctx context.Context, name string, members ...string) (bool, error) {
	if err := e.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-e.semaphore }()
	hook, err := e.hook(name, members)
	if err != nil {
		return false, nil
	}
	instance, err := e.getRuntime(ctx)
	if err != nil {
		return false, err
	}
	watch := watchRuntimeContext(instance.runtime, ctx, e.timeout, errors.New("plugin inspection timed out"))
	found, err := instance.runtime.Has(hook)
	watch.stop()
	instance.runtime.ClearInterrupt()
	if err == nil {
		e.putRuntime(instance)
	}
	return found, err
}

func (e *Engine) Call(ctx context.Context, name string, args ...any) (any, error) {
	return e.CallPath(ctx, name, nil, args...)
}
func (e *Engine) CallMember(ctx context.Context, name, member string, args ...any) (any, error) {
	return e.CallPath(ctx, name, []string{member}, args...)
}
func (e *Engine) CallInto(ctx context.Context, target any, name string, args ...any) error {
	result, err := e.Call(ctx, name, args...)
	if err != nil {
		return err
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		return err
	}
	return common.Unmarshal(encoded, target)
}

func (e *Engine) CallPath(ctx context.Context, name string, members []string, args ...any) (any, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	call := &runtimeCall{engine: e, ctx: ctx, holdsSlot: true}
	defer call.release()
	hook, err := e.hook(name, members)
	if err != nil {
		return nil, fmt.Errorf("plugin hook %q not found", name)
	}
	instance, err := e.getRuntime(ctx)
	if err != nil {
		return nil, err
	}
	call.runtime, call.hook = instance.runtime, hook.Name()
	call.fetcher, _ = ctx.Value(fetcherContextKey{}).(Fetcher)
	instance.call = call
	call.watchdog = watchRuntimeContext(instance.runtime, ctx, e.timeout, errors.New("plugin call timed out"))
	reusable := false
	defer func() {
		// Wait for an in-flight Interrupt before returning this runtime to the pool.
		call.watchdog.stop()
		instance.runtime.ClearInterrupt()
		instance.call = nil
		if reusable {
			instance.runtime.ReleaseCallData()
			e.putRuntime(instance)
		}
	}()
	values := make([]moejs.Value, len(args))
	for i, argument := range args {
		var encoded []byte
		if raw, ok := argument.(RawJSON); ok {
			encoded = raw
		} else {
			encoded, err = common.Marshal(argument)
		}
		if err == nil {
			values[i], err = instance.runtime.ParseJSONString(string(encoded))
		}
		if err != nil {
			return nil, fmt.Errorf("plugin hook %s argument %d: %w", hook.Name(), i+1, err)
		}
	}
	value, err := instance.runtime.Call(hook, values...)
	if err == nil {
		var result any
		result, err = instance.runtime.ToGo(value)
		if err == nil {
			encoded, marshalErr := common.Marshal(result)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if len(encoded) > MaxResultBytes {
				return nil, errors.New("plugin hook result exceeds 1 MiB")
			}
			reusable = true
			return result, nil
		}
	}
	var interrupted *moejs.InterruptedError
	if errors.As(err, &interrupted) {
		if cause, ok := interrupted.Value.(error); ok {
			return nil, fmt.Errorf("plugin hook %s interrupted: %w", hook.Name(), cause)
		}
	}
	var exception *moejs.Exception
	if errors.As(err, &exception) {
		message := []rune(exception.Message())
		if len(message) > 512 {
			message = message[:512]
		}
		for i, r := range message {
			if r < 32 || r >= 127 && r <= 159 {
				message[i] = ' '
			}
		}
		return nil, &HookError{Hook: hook.Name(), Message: string(message), wrapped: err}
	}
	return nil, fmt.Errorf("plugin hook %s failed: %w", hook.Name(), err)
}

func (e *Engine) hook(name string, members []string) (moejs.Hook, error) {
	key := name + "\x00" + strings.Join(members, "\x00")
	e.hooksMu.RLock()
	hook, found := e.hooks[key]
	e.hooksMu.RUnlock()
	if found {
		return hook, nil
	}
	hook, err := e.module.Hook(name, members...)
	if err == nil {
		e.hooksMu.Lock()
		if len(e.hooks) < 256 {
			e.hooks[key] = hook
		}
		e.hooksMu.Unlock()
	}
	return hook, err
}
func (e *Engine) putRuntime(instance *runtimeInstance) {
	select {
	case e.pool <- instance:
	default:
	}
}
func (e *Engine) getRuntime(ctx context.Context) (*runtimeInstance, error) {
	select {
	case instance := <-e.pool:
		return instance, nil
	default:
		return e.newRuntime(ctx)
	}
}
func (e *Engine) newRuntime(ctx context.Context) (*runtimeInstance, error) {
	runtime := moejs.NewRuntime(moejs.Options{DisableDynamicCode: true})
	instance := &runtimeInstance{runtime: runtime}
	if err := injectGlobals(runtime, e.now, e.log, func() *runtimeCall { return instance.call }); err != nil {
		return nil, err
	}
	watch := watchRuntimeContext(runtime, ctx, e.timeout, errors.New("plugin initialization timed out"))
	err := runtime.Load(e.module)
	watch.stop()
	runtime.ClearInterrupt()
	if err != nil {
		return nil, fmt.Errorf("evaluate plugin: %w", err)
	}
	return instance, nil
}

// Ported watchdog and fetch parking preserve the upstream execution budget.
// stop is idempotent, and synchronizes with a callback that already started.
type runtimeWatchdog struct {
	runtime      *moejs.Runtime
	ctx          context.Context
	timeoutError error
	remaining    time.Duration
	started      time.Time
	watch        context.Context
	stopWatch    func() bool
	cancel       context.CancelFunc
	interrupted  chan struct{}
	paused       bool
}

func watchRuntimeContext(runtime *moejs.Runtime, ctx context.Context, timeout time.Duration, timeoutError error) *runtimeWatchdog {
	w := &runtimeWatchdog{runtime: runtime, ctx: ctx, timeoutError: timeoutError, remaining: timeout}
	w.start()
	return w
}
func (w *runtimeWatchdog) start() {
	watch, cancel := context.WithTimeoutCause(w.ctx, w.remaining, w.timeoutError)
	interrupted := make(chan struct{})
	w.stopWatch = context.AfterFunc(watch, func() { w.runtime.Interrupt(context.Cause(watch)); close(interrupted) })
	w.watch, w.cancel, w.interrupted = watch, cancel, interrupted
	w.started, w.paused = time.Now(), false
}
func (w *runtimeWatchdog) stop() {
	if w.paused {
		return
	}
	if !w.stopWatch() {
		<-w.interrupted
	}
	w.cancel()
	w.paused = true
}
func (w *runtimeWatchdog) pause() bool {
	if !w.stopWatch() {
		<-w.interrupted
		return false
	}
	w.cancel()
	w.remaining -= time.Since(w.started)
	w.paused = true
	return true
}
func (w *runtimeWatchdog) resume() {
	w.start()
	if w.watch.Err() != nil {
		<-w.interrupted
	}
}

func sourceWithoutCommentsAndStrings(source string) string {
	var output strings.Builder
	output.Grow(len(source))
	quote := byte(0)
	escaped, lineComment, blockComment := false, false, false
	for i := 0; i < len(source); i++ {
		current, next := source[i], byte(0)
		if i+1 < len(source) {
			next = source[i+1]
		}
		if lineComment {
			if current == '\n' {
				lineComment = false
				output.WriteByte('\n')
			} else {
				output.WriteByte(' ')
			}
			continue
		}
		if blockComment {
			if current == '*' && next == '/' {
				blockComment = false
				output.WriteString("  ")
				i++
			} else {
				output.WriteByte(' ')
			}
			continue
		}
		if quote != 0 {
			output.WriteByte(' ')
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == quote {
				quote = 0
			}
			continue
		}
		if current == '/' && next == '/' {
			lineComment = true
			output.WriteString("  ")
			i++
			continue
		}
		if current == '/' && next == '*' {
			blockComment = true
			output.WriteString("  ")
			i++
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			output.WriteByte(' ')
			continue
		}
		output.WriteByte(current)
	}
	return output.String()
}
