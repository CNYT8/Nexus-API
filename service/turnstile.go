package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

var (
	ErrTurnstileNotConfigured      = errors.New("turnstile is not configured")
	ErrTurnstileTokenRequired      = errors.New("turnstile token is required")
	ErrTurnstileVerificationFailed = errors.New("turnstile verification failed")
)

type turnstileCheckResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

func VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	if !common.TurnstileConfigured() {
		return ErrTurnstileNotConfigured
	}
	client := &http.Client{Timeout: 8 * time.Second}
	return verifyTurnstileWithClient(
		ctx,
		client,
		turnstileVerifyURL,
		common.TurnstileSecretKey,
		token,
		remoteIP,
	)
}

func verifyTurnstileWithClient(ctx context.Context, client *http.Client, endpoint string, secret string, token string, remoteIP string) error {
	if client == nil || strings.TrimSpace(endpoint) == "" || strings.TrimSpace(secret) == "" {
		return ErrTurnstileNotConfigured
	}
	if strings.TrimSpace(token) == "" {
		return ErrTurnstileTokenRequired
	}
	form := url.Values{
		"secret":   {secret},
		"response": {token},
	}
	if strings.TrimSpace(remoteIP) != "" {
		form.Set("remoteip", remoteIP)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("turnstile verification returned HTTP %d", response.StatusCode)
	}
	var result turnstileCheckResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return err
	}
	if !result.Success {
		return ErrTurnstileVerificationFailed
	}
	return nil
}
