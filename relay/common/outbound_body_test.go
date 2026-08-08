package common

import (
	"io"
	"testing"

	basecommon "github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutboundJSONBodyReplaysIndependentBodies(t *testing.T) {
	payload := []byte(`{"model":"test-model","input":"abcdefghijklmnopqrstuvwxyz"}`)
	body, closer, err := NewOutboundJSONBody(payload)
	require.NoError(t, err)
	defer closer.Close()

	primaryHead := make([]byte, 10)
	_, err = io.ReadFull(body, primaryHead)
	require.NoError(t, err)

	a, err := body.NewReader()
	require.NoError(t, err)
	b, err := body.NewReader()
	require.NoError(t, err)

	aHead := make([]byte, 10)
	_, err = io.ReadFull(a, aHead)
	require.NoError(t, err)
	bAll, err := io.ReadAll(b)
	require.NoError(t, err)
	require.NoError(t, b.Close())
	aRest, err := io.ReadAll(a)
	require.NoError(t, err)
	require.NoError(t, a.Close())

	assert.Equal(t, payload, bAll)
	assert.Equal(t, payload[10:], aRest)

	require.NoError(t, closer.Close())
	_, err = body.NewReader()
	require.ErrorIs(t, err, basecommon.ErrStorageClosed)
}
