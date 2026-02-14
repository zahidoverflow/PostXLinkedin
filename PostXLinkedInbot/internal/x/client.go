package x

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

func New(httpClient *http.Client, baseURL string, userBearerToken string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{httpClient: httpClient, baseURL: baseURL, token: userBearerToken}
}

type UploadMediaRequest struct {
	Media        string `json:"media"` // base64
	MediaType    string `json:"media_type,omitempty"`
	Category     string `json:"media_category,omitempty"` // e.g. "tweet_image"
	AltText      string `json:"alt_text,omitempty"`
	Shared       bool   `json:"shared,omitempty"`
	OwnerID      string `json:"owner_id,omitempty"`
	AdditionalID string `json:"additional_owners,omitempty"`
}

type UploadMediaResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (c *Client) UploadMedia(ctx context.Context, base64Media string, mediaType string) (string, error) {
	reqBody := UploadMediaRequest{
		Media:     base64Media,
		MediaType: mediaType,
		Category:  "tweet_image",
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/2/media/upload", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res, 8<<10)
		return "", fmt.Errorf("x upload media failed: %s: %s", res.Status, body)
	}

	var ur UploadMediaResponse
	if err := json.NewDecoder(res.Body).Decode(&ur); err != nil {
		return "", err
	}
	if ur.Data.ID == "" {
		return "", fmt.Errorf("x upload media: missing id")
	}
	return ur.Data.ID, nil
}

type CreatePostRequest struct {
	Text  string `json:"text"`
	Media *struct {
		MediaIDs []string `json:"media_ids"`
	} `json:"media,omitempty"`
}

type CreatePostResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (c *Client) CreatePost(ctx context.Context, text string, mediaIDs []string) (string, error) {
	reqBody := CreatePostRequest{Text: text}
	if len(mediaIDs) > 0 {
		reqBody.Media = &struct {
			MediaIDs []string `json:"media_ids"`
		}{MediaIDs: mediaIDs}
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/2/tweets", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res, 8<<10)
		return "", fmt.Errorf("x create post failed: %s: %s", res.Status, body)
	}

	var cr CreatePostResponse
	if err := json.NewDecoder(res.Body).Decode(&cr); err != nil {
		return "", err
	}
	if cr.Data.ID == "" {
		return "", fmt.Errorf("x create post: missing id")
	}
	return cr.Data.ID, nil
}

func readSmall(res *http.Response, limit int64) (string, error) {
	lr := &io.LimitedReader{R: res.Body, N: limit}
	b, err := ioReadAll(lr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
