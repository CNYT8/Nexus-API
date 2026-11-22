package jsplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Calcium-Ion/moejs"
	"github.com/QuantumNous/new-api/common"
)

// MarshalJSON allows ordered values inside contexts, not only at their root.
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if !json.Valid(r) {
		return nil, errors.New("invalid ordered JSON")
	}
	return r, nil
}

func (m Meta) PreservesJSONOrder() bool {
	for _, capability := range m.RequiredCapabilities {
		if capability == CapabilityJSONOrder {
			return true
		}
	}
	return false
}

func (p *LoadedPlugin) UsesSubmitEventDelta() bool {
	for _, capability := range p.Meta.RequiredCapabilities {
		if capability == CapabilitySubmitSSEDelta {
			return true
		}
	}
	return p.Engine.HasExport("parseSubmitEventDelta") && !p.Engine.HasExport("parseSubmitEvent")
}

// CallJSON uses the runtime's JSON.stringify semantics before crossing into Go.
// It retains object insertion order, omits undefined members, and shares the
// ordinary hook admission, cancellation and parked-fetch execution budget.
func (e *Engine) CallJSON(ctx context.Context, name string, members []string, args ...any) (RawJSON, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	call := &runtimeCall{engine: e, ctx: ctx, holdsSlot: true}
	defer call.release()
	hook, err := e.hook(name, members)
	if err != nil {
		return nil, err
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
		encoded, err := common.Marshal(argument)
		if err != nil {
			return nil, err
		}
		values[i], err = instance.runtime.ParseJSONString(string(encoded))
		if err != nil {
			return nil, fmt.Errorf("plugin hook argument %d: %w", i+1, err)
		}
	}
	value, err := instance.runtime.Call(hook, values...)
	if err != nil {
		return nil, fmt.Errorf("plugin ordered hook %s failed: %w", name, err)
	}
	result, err := instance.runtime.AppendJSON(nil, value)
	if err != nil {
		return nil, fmt.Errorf("plugin ordered hook %s JSON failed: %w", name, err)
	}
	if len(result) == 0 || len(result) > MaxResultBytes {
		return nil, errors.New("plugin ordered result is undefined or exceeds 1 MiB")
	}
	reusable = true
	return RawJSON(result), nil
}
