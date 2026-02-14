package linkedin

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
	token      string
	version    string
}

func New(httpClient *http.Client, accessToken string, linkedInVersion string) *Client {
	v := strings.TrimSpace(linkedInVersion)
	if v == "" {
		v = "202404"
	}
	return &Client{httpClient: httpClient, token: accessToken, version: v}
}

type initUploadReq struct {
	InitializeUploadRequest struct {
		Owner string `json:"owner"`
	} `json:"initializeUploadRequest"`
}

type initUploadResp struct {
	Value struct {
		UploadURL string `json:"uploadUrl"`
		Image     string `json:"image"` // urn
	} `json:"value"`
}

func (c *Client) InitializeImageUpload(ctx context.Context, ownerURN string) (uploadURL string, imageURN string, err error) {
	var reqBody initUploadReq
	reqBody.InitializeUploadRequest.Owner = ownerURN
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.linkedin.com/rest/images?action=initializeUpload", bytes.NewReader(b))
	if err != nil {
		return "", "", err
	}
	c.addHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res.Body, 8<<10)
		return "", "", fmt.Errorf("linkedin init upload failed: %s: %s", res.Status, body)
	}

	var ir initUploadResp
	if err := json.NewDecoder(res.Body).Decode(&ir); err != nil {
		return "", "", err
	}
	if ir.Value.UploadURL == "" || ir.Value.Image == "" {
		return "", "", fmt.Errorf("linkedin init upload missing fields")
	}
	return ir.Value.UploadURL, ir.Value.Image, nil
}

func (c *Client) UploadImageBytes(ctx context.Context, uploadURL string, mimeType string, image []byte) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(image))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mimeType)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res.Body, 8<<10)
		return fmt.Errorf("linkedin upload failed: %s: %s", res.Status, body)
	}
	return nil
}

type createPostReq struct {
	Author                    string `json:"author"`
	Commentary                string `json:"commentary"`
	Visibility                string `json:"visibility"`
	Distribution              any    `json:"distribution,omitempty"`
	Content                   any    `json:"content,omitempty"`
	LifecycleState            string `json:"lifecycleState,omitempty"`
	IsReshareDisabledByAuthor bool   `json:"isReshareDisabledByAuthor,omitempty"`
}

type createPostResp struct {
	ID string `json:"id"`
}

func (c *Client) CreateImagePost(ctx context.Context, authorURN string, caption string, imageURN string, title string) (string, error) {
	reqBody := createPostReq{
		Author:     authorURN,
		Commentary: caption,
		Visibility: "PUBLIC",
		Distribution: map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		Content: map[string]any{
			"media": map[string]any{
				"title": title,
				"id":    imageURN,
			},
		},
		LifecycleState:            "PUBLISHED",
		IsReshareDisabledByAuthor: false,
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.linkedin.com/rest/posts", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	c.addHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res.Body, 12<<10)
		return "", fmt.Errorf("linkedin create post failed: %s: %s", res.Status, body)
	}

	var pr createPostResp
	_ = json.NewDecoder(res.Body).Decode(&pr)
	if pr.ID == "" {
		// Some LinkedIn APIs may respond differently; if so, still treat as success by status code.
		return "ok", nil
	}
	return pr.ID, nil
}

func (c *Client) addHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("LinkedIn-Version", c.version)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
}

func readSmall(r io.Reader, limit int64) (string, error) {
	lr := &io.LimitedReader{R: r, N: limit}
	b, err := io.ReadAll(lr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
