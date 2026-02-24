package telegram

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	getFileRetries  = 3
	downloadRetries = 3
	retryBaseDelay  = 200 * time.Millisecond
)

type Client struct {
	Bot   *bot.Bot
	Token string
}

func New(token string) (*Client, error) {
	b, err := bot.New(strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}
	return &Client{Bot: b, Token: strings.TrimSpace(token)}, nil
}

func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, string, error) {
	var (
		file *models.File
		err  error
	)

	for attempt := 1; attempt <= getFileRetries; attempt++ {
		file, err = c.Bot.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
		if err == nil {
			break
		}
		if attempt == getFileRetries || ctx.Err() != nil {
			return nil, "", err
		}
		if sleepErr := sleepRetry(ctx, attempt); sleepErr != nil {
			return nil, "", sleepErr
		}
	}

	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.Token, file.FilePath)
	client := &http.Client{Timeout: 25 * time.Second}
	var lastErr error

	for attempt := 1; attempt <= downloadRetries; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			return nil, "", reqErr
		}

		resp, httpErr := client.Do(req)
		if httpErr != nil {
			lastErr = httpErr
		} else {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK {
				return data, file.FilePath, nil
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("telegram file download status: %d", resp.StatusCode)
			}
			if !shouldRetryStatus(resp.StatusCode) {
				return nil, "", lastErr
			}
		}

		if attempt == downloadRetries || ctx.Err() != nil {
			break
		}
		if !shouldRetryError(lastErr) {
			break
		}
		if sleepErr := sleepRetry(ctx, attempt); sleepErr != nil {
			return nil, "", sleepErr
		}
	}

	return nil, "", lastErr
}

func (c *Client) Start(ctx context.Context) {
	c.Bot.Start(ctx)
}

func (c *Client) Stop() {}

func sleepRetry(ctx context.Context, attempt int) error {
	delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func shouldRetryStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func shouldRetryError(err error) bool {
	if err == nil {
		return false
	}
	if nerr, ok := err.(net.Error); ok {
		return nerr.Timeout() || nerr.Temporary()
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor") || strings.Contains(msg, "reset")
}
