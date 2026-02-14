package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	httpClient   *http.Client
	webhookURL   string
	sharedSecret string
}

func New(httpClient *http.Client, webhookURL string, sharedSecret string) *Client {
	return &Client{httpClient: httpClient, webhookURL: webhookURL, sharedSecret: sharedSecret}
}

type ProcessRequest struct {
	Caption string   `json:"caption"`
	Targets []string `json:"targets,omitempty"` // e.g. ["x","linkedin"]
}

type ProcessResponse struct {
	OK      bool   `json:"ok"`
	Caption string `json:"caption,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (c *Client) ProcessCaption(ctx context.Context, caption string, targets []string) (string, error) {
	reqBody := ProcessRequest{Caption: caption, Targets: targets}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.webhookURL, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.sharedSecret != "" {
		req.Header.Set("X-PostXLinkedIn-Agent-Secret", c.sharedSecret)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(&io.LimitedReader{R: res.Body, N: 8 << 10})
		return "", fmt.Errorf("agent webhook failed: %s: %s", res.Status, string(body))
	}

	var pr ProcessResponse
	if err := json.NewDecoder(res.Body).Decode(&pr); err != nil {
		return "", err
	}
	if !pr.OK {
		if pr.Error == "" {
			pr.Error = "agent returned ok=false"
		}
		return "", errors.New(pr.Error)
	}
	if pr.Caption == "" {
		// Treat as "no change".
		return caption, nil
	}
	return pr.Caption, nil
}
