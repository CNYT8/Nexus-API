package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type turnstileRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn turnstileRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestTurnstileVerifierSendsTokenAndRemoteIP(t *testing.T) {
	client := &http.Client{Transport: turnstileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.NoError(t, request.ParseForm())
		assert.Equal(t, "secret-key", request.Form.Get("secret"))
		assert.Equal(t, "challenge-token", request.Form.Get("response"))
		assert.Equal(t, "203.0.113.8", request.Form.Get("remoteip"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	err := verifyTurnstileWithClient(context.Background(), client, "https://turnstile.test/verify", "secret-key", "challenge-token", "203.0.113.8")
	require.NoError(t, err)
}

func TestTurnstileVerifierRejectsFailedChallenge(t *testing.T) {
	client := &http.Client{Transport: turnstileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":false,"error-codes":["invalid-input-response"]}`)),
			Header:     make(http.Header),
		}, nil
	})}

	err := verifyTurnstileWithClient(context.Background(), client, "https://turnstile.test/verify", "secret-key", "invalid-token", "203.0.113.8")
	require.ErrorIs(t, err, ErrTurnstileVerificationFailed)
}
