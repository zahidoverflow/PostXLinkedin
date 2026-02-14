package n8n

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Client struct {
	httpClient   *http.Client
	webhookURL   string
	sharedSecret string
}

func NewClient(httpClient *http.Client, webhookURL string, sharedSecret string) *Client {
	return &Client{httpClient: httpClient, webhookURL: webhookURL, sharedSecret: sharedSecret}
}

type TelegramFrom struct {
	ID        int64  `json:"id"`
	UserName  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type TelegramMeta struct {
	ChatID    int64        `json:"chat_id"`
	MessageID int          `json:"message_id"`
	From      TelegramFrom `json:"from"`
}

type PostRequest struct {
	Caption       string       `json:"caption"`
	ImageBase64   string       `json:"image_base64"`
	ImageMIME     string       `json:"image_mime"`
	ImageFilename string       `json:"image_filename"`
	Telegram      TelegramMeta `json:"telegram"`
}

type PostResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *Client) Post(ctx context.Context, req PostRequest) (PostResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return PostResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.webhookURL, bytes.NewReader(body))
	if err != nil {
		return PostResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.sharedSecret != "" {
		httpReq.Header.Set("X-PostXLinkedIn-Secret", c.sharedSecret)
	}

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return PostResponse{}, err
	}
	defer res.Body.Close()

	// n8n webhook may return arbitrary JSON; we keep a small contract for the workflow to honor.
	var pr PostResponse
	if err := json.NewDecoder(res.Body).Decode(&pr); err != nil {
		return PostResponse{}, fmt.Errorf("decode n8n response (status=%s): %w", res.Status, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if pr.Error == "" {
			pr.Error = res.Status
		}
		pr.OK = false
		return pr, fmt.Errorf("n8n status=%s error=%s", res.Status, pr.Error)
	}
	return pr, nil
}
