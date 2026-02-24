package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func downloadWithHeaders(ctx context.Context, sourceURL, referer string) ([]byte, error) {
	return downloadWithHeadersTimeout(ctx, sourceURL, referer, 45*time.Second)
}

func downloadWithHeadersTimeout(ctx context.Context, sourceURL, referer string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func downloadWithHeadersRetry(ctx context.Context, sourceURL, referer string, timeout time.Duration, retries int, backoff time.Duration) ([]byte, error) {
	if retries < 0 {
		retries = 0
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	if backoff <= 0 {
		backoff = time.Second
	}
	attempts := retries + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		data, err := downloadWithHeadersTimeout(ctx, sourceURL, referer, timeout)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if i >= retries || !isRetryableDownloadErr(err) {
			break
		}
		if waitErr := sleepWithContext(ctx, backoff*time.Duration(i+1)); waitErr != nil {
			break
		}
	}
	return nil, lastErr
}

func isRetryableDownloadErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor") || strings.Contains(msg, "reset")
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func maxInt(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
