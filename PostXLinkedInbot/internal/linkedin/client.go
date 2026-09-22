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
	baseURL    string
}

func New(httpClient *http.Client, accessToken string, linkedInVersion string) *Client {
	v := strings.TrimSpace(linkedInVersion)
	if v == "" {
		v = "202601"
	}
	return &Client{httpClient: httpClient, token: accessToken, version: v, baseURL: "https://api.linkedin.com"}
}

func (c *Client) SetBaseURL(u string) {
	c.baseURL = strings.TrimRight(u, "/")
}

func (c *Client) endpoint(p string) string {
	base := c.baseURL
	if base == "" {
		base = "https://api.linkedin.com"
	}
	return strings.TrimRight(base, "/") + p
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

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/images?action=initializeUpload"), bytes.NewReader(b))
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

type initDocUploadResp struct {
	Value struct {
		UploadURL string `json:"uploadUrl"`
		Document  string `json:"document"` // urn:li:document:...
	} `json:"value"`
}

// InitializeDocumentUpload registers an upcoming document upload on LinkedIn.
// Supports PDF, DOC/DOCX, PPT/PPTX, ODT, ODS, PPSX per LinkedIn docs.
func (c *Client) InitializeDocumentUpload(ctx context.Context, ownerURN string) (uploadURL string, docURN string, err error) {
	var reqBody initUploadReq
	reqBody.InitializeUploadRequest.Owner = ownerURN
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/documents?action=initializeUpload"), bytes.NewReader(b))
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
		return "", "", fmt.Errorf("linkedin init document upload failed: %s: %s", res.Status, body)
	}

	var ir initDocUploadResp
	if err := json.NewDecoder(res.Body).Decode(&ir); err != nil {
		return "", "", err
	}
	if ir.Value.UploadURL == "" || ir.Value.Document == "" {
		return "", "", fmt.Errorf("linkedin init document upload missing fields")
	}
	return ir.Value.UploadURL, ir.Value.Document, nil
}

// UploadDocumentBytes uploads document binary data to LinkedIn's signed upload URL.
func (c *Client) UploadDocumentBytes(ctx context.Context, uploadURL string, mimeType string, docBytes []byte) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(docBytes))
	if err != nil {
		return err
	}
	if mimeType != "" {
		req.Header.Set("Content-Type", mimeType)
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res.Body, 8<<10)
		return fmt.Errorf("linkedin document upload failed: %s: %s", res.Status, body)
	}
	return nil
}

type initVideoUploadReq struct {
	InitializeUploadRequest struct {
		Owner           string `json:"owner"`
		FileSizeBytes   int64  `json:"fileSizeBytes"`
		UploadCaptions  bool   `json:"uploadCaptions"`
		UploadThumbnail bool   `json:"uploadThumbnail"`
	} `json:"initializeUploadRequest"`
}

type VideoUploadInstruction struct {
	UploadURL string `json:"uploadUrl"`
	FirstByte int64  `json:"firstByte"`
	LastByte  int64  `json:"lastByte"`
}

type initVideoUploadResp struct {
	Value struct {
		Video              string                   `json:"video"` // urn:li:video:...
		UploadInstructions []VideoUploadInstruction `json:"uploadInstructions"`
		UploadToken        string                   `json:"uploadToken"`
	} `json:"value"`
}

type finalizeVideoUploadReq struct {
	FinalizeUploadRequest struct {
		Video           string   `json:"video"`
		UploadToken     string   `json:"uploadToken"`
		UploadedPartIds []string `json:"uploadedPartIds"`
	} `json:"finalizeUploadRequest"`
}

// InitializeVideoUpload registers a video upload on LinkedIn.
// Supports MP4, MOV, AVI, WEBM, MKV, WMV, etc. per LinkedIn docs.
func (c *Client) InitializeVideoUpload(ctx context.Context, ownerURN string, fileSizeBytes int64) (videoURN string, uploadToken string, instructions []VideoUploadInstruction, err error) {
	var reqBody initVideoUploadReq
	reqBody.InitializeUploadRequest.Owner = ownerURN
	reqBody.InitializeUploadRequest.FileSizeBytes = fileSizeBytes
	reqBody.InitializeUploadRequest.UploadCaptions = false
	reqBody.InitializeUploadRequest.UploadThumbnail = false
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/videos?action=initializeUpload"), bytes.NewReader(b))
	if err != nil {
		return "", "", nil, err
	}
	c.addHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res.Body, 8<<10)
		return "", "", nil, fmt.Errorf("linkedin init video upload failed: %s: %s", res.Status, body)
	}

	var vr initVideoUploadResp
	if err := json.NewDecoder(res.Body).Decode(&vr); err != nil {
		return "", "", nil, err
	}
	if vr.Value.Video == "" || len(vr.Value.UploadInstructions) == 0 {
		return "", "", nil, fmt.Errorf("linkedin init video upload missing fields")
	}
	return vr.Value.Video, vr.Value.UploadToken, vr.Value.UploadInstructions, nil
}

// UploadVideoParts uploads the video chunks specified by instructions and collects ETag headers.
func (c *Client) UploadVideoParts(ctx context.Context, instructions []VideoUploadInstruction, videoBytes []byte) ([]string, error) {
	var etags []string
	total := int64(len(videoBytes))

	for i, inst := range instructions {
		start := inst.FirstByte
		end := inst.LastByte + 1
		if start < 0 {
			start = 0
		}
		if end > total {
			end = total
		}
		if start >= end {
			return nil, fmt.Errorf("invalid byte range [%d, %d] for part %d", start, end, i)
		}
		chunk := videoBytes[start:end]

		req, err := http.NewRequestWithContext(ctx, "PUT", inst.UploadURL, bytes.NewReader(chunk))
		if err != nil {
			return nil, fmt.Errorf("create video part request %d: %w", i, err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")

		res, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("upload video part %d: %w", i, err)
		}
		defer res.Body.Close()

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			body, _ := readSmall(res.Body, 8<<10)
			return nil, fmt.Errorf("upload video part %d failed: %s: %s", i, res.Status, body)
		}

		etag := res.Header.Get("ETag")
		if etag == "" {
			etag = res.Header.Get("etag")
		}
		etags = append(etags, etag)
	}

	return etags, nil
}

// FinalizeVideoUpload informs LinkedIn that all video parts have been uploaded.
func (c *Client) FinalizeVideoUpload(ctx context.Context, videoURN string, uploadToken string, etags []string) error {
	var reqBody finalizeVideoUploadReq
	reqBody.FinalizeUploadRequest.Video = videoURN
	reqBody.FinalizeUploadRequest.UploadToken = uploadToken
	reqBody.FinalizeUploadRequest.UploadedPartIds = etags
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/videos?action=finalizeUpload"), bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.addHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := readSmall(res.Body, 8<<10)
		return fmt.Errorf("linkedin finalize video upload failed: %s: %s", res.Status, body)
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

func sanitizeTitle(t string) string {
	t = strings.ReplaceAll(t, "\r", "")
	t = strings.ReplaceAll(t, "\n", " ")
	t = strings.TrimSpace(t)
	runes := []rune(t)
	if len(runes) > 100 {
		t = string(runes[:97]) + "..."
	}
	if t == "" {
		t = "Media"
	}
	return t
}

func sanitizeDescription(d string) string {
	d = strings.ReplaceAll(d, "\r", "")
	d = strings.ReplaceAll(d, "\n", " ")
	d = strings.TrimSpace(d)
	runes := []rune(d)
	if len(runes) > 400 {
		d = string(runes[:397]) + "..."
	}
	return d
}

func (c *Client) createMediaPost(ctx context.Context, authorURN string, caption string, mediaURN string, title string) (string, error) {
	reqBody := createPostReq{
		Author:     authorURN,
		Commentary: sanitizeCommentary(caption),
		Visibility: "PUBLIC",
		Distribution: map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		Content: map[string]any{
			"media": map[string]any{
				"title": sanitizeTitle(title),
				"id":    mediaURN,
			},
		},
		LifecycleState:            "PUBLISHED",
		IsReshareDisabledByAuthor: false,
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/posts"), bytes.NewReader(b))
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

	// LinkedIn returns the post ID in the x-restli-id response header.
	if id := res.Header.Get("x-restli-id"); id != "" {
		return id, nil
	}
	// Fallback: try parsing the JSON body.
	var pr struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&pr)
	if pr.ID != "" {
		return pr.ID, nil
	}
	return "ok", nil
}

func (c *Client) CreateImagePost(ctx context.Context, authorURN string, caption string, imageURN string, title string) (string, error) {
	if title == "" {
		title = "Image"
	}
	return c.createMediaPost(ctx, authorURN, caption, imageURN, title)
}

func (c *Client) CreateDocumentPost(ctx context.Context, authorURN string, caption string, docURN string, title string) (string, error) {
	if title == "" {
		title = "Document"
	}
	return c.createMediaPost(ctx, authorURN, caption, docURN, title)
}

func (c *Client) CreateVideoPost(ctx context.Context, authorURN string, caption string, videoURN string, title string) (string, error) {
	if title == "" {
		title = "Video"
	}
	return c.createMediaPost(ctx, authorURN, caption, videoURN, title)
}

// CreateTextPost creates a text-only post (no media) on LinkedIn.
func (c *Client) CreateTextPost(ctx context.Context, authorURN string, text string) (string, error) {
	reqBody := createPostReq{
		Author:     authorURN,
		Commentary: sanitizeCommentary(text),
		Visibility: "PUBLIC",
		Distribution: map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		LifecycleState:            "PUBLISHED",
		IsReshareDisabledByAuthor: false,
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/posts"), bytes.NewReader(b))
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
		return "", fmt.Errorf("linkedin create text post failed: %s: %s", res.Status, body)
	}
	if id := res.Header.Get("x-restli-id"); id != "" {
		return id, nil
	}
	return "ok", nil
}

type articleContent struct {
	Source      string `json:"source"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Thumbnail   string `json:"thumbnail,omitempty"`
}

type createArticlePostReq struct {
	Author       string `json:"author"`
	Commentary   string `json:"commentary"`
	Visibility   string `json:"visibility"`
	Distribution any    `json:"distribution,omitempty"`
	Content      *struct {
		Article articleContent `json:"article"`
	} `json:"content,omitempty"`
	LifecycleState            string `json:"lifecycleState,omitempty"`
	IsReshareDisabledByAuthor bool   `json:"isReshareDisabledByAuthor,omitempty"`
}

// CreateArticlePost posts a URL to LinkedIn, generating an interactive rich link preview card.
func (c *Client) CreateArticlePost(ctx context.Context, authorURN string, caption string, articleURL string, title string, description string, thumbnailURN string) (string, error) {
	if title == "" {
		title = articleURL
	}
	reqBody := createArticlePostReq{
		Author:     authorURN,
		Commentary: sanitizeCommentary(caption),
		Visibility: "PUBLIC",
		Distribution: map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		Content: &struct {
			Article articleContent `json:"article"`
		}{
			Article: articleContent{
				Source:      articleURL,
				Title:       sanitizeTitle(title),
				Description: sanitizeDescription(description),
				Thumbnail:   thumbnailURN,
			},
		},
		LifecycleState:            "PUBLISHED",
		IsReshareDisabledByAuthor: false,
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint("/rest/posts"), bytes.NewReader(b))
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
		return "", fmt.Errorf("linkedin create article post failed: %s: %s", res.Status, body)
	}

	if id := res.Header.Get("x-restli-id"); id != "" {
		return id, nil
	}
	var pr struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&pr)
	if pr.ID != "" {
		return pr.ID, nil
	}
	return "ok", nil
}

func sanitizeCommentary(s string) string {
	// LinkedIn's commentary field supports a specialized Markdown syntax.
	// Reserved characters must be escaped with a backslash to be treated as literals.
	// We escape the most common syntax characters that can break the parser.
	r := strings.NewReplacer(
		"\\", "\\\\",
		"|", "\\|",
		"(", "\\(",
		")", "\\)",
		"[", "\\[",
		"]", "\\]",
		"{", "\\{",
		"}", "\\}",
		"<", "\\<",
		">", "\\>",
	)
	return r.Replace(s)
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
