package channel

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	basecommon "github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyUpstreamBodyMetadataSetsReplayableMetadata(t *testing.T) {
	payload := []byte(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
	body, closer, err := relaycommon.NewOutboundJSONBody(payload)
	require.NoError(t, err)
	defer closer.Close()

	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/chat/completions", body)
	require.NoError(t, err)
	assert.Nil(t, req.GetBody)
	assert.Zero(t, req.ContentLength)

	ApplyUpstreamBodyMetadata(req, body)
	assert.EqualValues(t, len(payload), req.ContentLength)
	require.NotNil(t, req.GetBody)

	_, err = io.ReadAll(req.Body)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		rc, err := req.GetBody()
		require.NoError(t, err)
		replay, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, payload, replay)
	}
}

func TestApplyUpstreamBodyMetadataHidesStorageCloser(t *testing.T) {
	payload := []byte("raw storage")
	storage, err := basecommon.CreateBodyStorage(payload)
	require.NoError(t, err)
	defer storage.Close()

	req, err := http.NewRequest(http.MethodPost, "https://example.com", storage)
	require.NoError(t, err)
	ApplyUpstreamBodyMetadata(req, storage)
	require.NoError(t, req.Body.Close())

	rc, err := req.GetBody()
	require.NoError(t, err)
	replay, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, payload, replay)
}

func TestApplyUpstreamBodyMetadataKeepsNativeGetBody(t *testing.T) {
	body := bytes.NewReader([]byte("original"))
	req, err := http.NewRequest(http.MethodPost, "https://example.com", body)
	require.NoError(t, err)
	originalGetBody := req.GetBody

	ApplyUpstreamBodyMetadata(req, body)
	require.NotNil(t, req.GetBody)
	rc, err := originalGetBody()
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, "original", string(got))
}
