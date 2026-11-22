package dto

import "strconv"

// ToolUsageCounter unions actual tool invocations from incremental events and
// terminal snapshots. Declarations in request.tools must never be recorded.
// One counter belongs to one response; it is not shared between requests.
type ToolUsageCounter struct {
	calls   map[string]string
	byIndex map[int]string
}

// Record accepts the billing tool key, an output item ID, and output_index.
// An index-only event is reconciled when a later snapshot supplies its ID.
func (c *ToolUsageCounter) Record(kind, id string, index *int) {
	if kind == "" || (id == "" && index == nil) {
		return
	}
	if c.calls == nil {
		c.calls = make(map[string]string)
		c.byIndex = make(map[int]string)
	}
	key := "id:" + id
	if id == "" {
		key = c.byIndex[*index]
		if key == "" {
			key = "index:" + strconv.Itoa(*index)
		}
	}
	if index != nil {
		if old := c.byIndex[*index]; old != "" && old != key {
			delete(c.calls, old)
		}
		c.byIndex[*index] = key
	}
	c.calls[key] = kind
}

// Counts returns a copy, suitable for Usage.ToolUsage and legacy CallCount.
func (c *ToolUsageCounter) Counts() map[string]int {
	if len(c.calls) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, kind := range c.calls {
		counts[kind]++
	}
	return counts
}
