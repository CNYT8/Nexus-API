package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesSnapshotDeltaRequiresSentPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, previous, snapshot, delta string
	}{
		{"terminal only", "", "visible", "visible"},
		{"unseen suffix", "visible", "visible tail", " tail"},
		{"duplicate", "visible", "visible", ""},
		{"older snapshot", "visible tail", "visible", ""},
		{"conflicting snapshot", "visible<thi", "visible", ""},
		{"different snapshot", "visible", "replacement", ""},
		{"missing snapshot", "visible", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.delta, stringDeltaFromPrefix(tc.previous, tc.snapshot))
		})
	}
}
