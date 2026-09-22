package opengraph

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Metadata contains open graph and HTML metadata extracted from a URL.
type Metadata struct {
	Title       string
	Description string
	ImageURL    string
}

var (
	metaTagRegex = regexp.MustCompile(`(?is)<meta\s+([^>]+)>`)
	attrRegex    = regexp.MustCompile(`(?i)([a-z0-9_\-:]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	titleRegex   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

// Extract parses metadata from raw HTML bytes.
func Extract(htmlContent []byte, pageURL string) *Metadata {
	text := string(htmlContent)
	m := &Metadata{}

	matches := metaTagRegex.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		rawAttrs := match[1]
		attrPairs := attrRegex.FindAllStringSubmatch(rawAttrs, -1)
		attrs := make(map[string]string)
		for _, pair := range attrPairs {
			if len(pair) < 2 {
				continue
			}
			k := strings.ToLower(pair[1])
			val := ""
			if len(pair) > 2 && pair[2] != "" {
				val = pair[2]
			} else if len(pair) > 3 && pair[3] != "" {
				val = pair[3]
			} else if len(pair) > 4 {
				val = pair[4]
			}
			attrs[k] = val
		}

		key := strings.ToLower(attrs["property"])
		if key == "" {
			key = strings.ToLower(attrs["name"])
		}
		content := attrs["content"]
		if key == "" || content == "" {
			continue
		}

		switch key {
		case "og:title":
			if m.Title == "" {
				m.Title = content
			}
		case "twitter:title":
			if m.Title == "" {
				m.Title = content
			}
		case "og:description":
			if m.Description == "" {
				m.Description = content
			}
		case "twitter:description", "description":
			if m.Description == "" {
				m.Description = content
			}
		case "og:image", "og:image:url", "twitter:image", "twitter:image:src":
			if m.ImageURL == "" {
				m.ImageURL = content
			}
		}
	}

	if m.Title == "" {
		if tm := titleRegex.FindStringSubmatch(text); len(tm) > 1 {
			m.Title = tm[1]
		}
	}

	m.Title = strings.TrimSpace(html.UnescapeString(m.Title))
	m.Description = strings.TrimSpace(html.UnescapeString(m.Description))
	m.ImageURL = strings.TrimSpace(html.UnescapeString(m.ImageURL))

	// Resolve relative image URLs against the page URL.
	if m.ImageURL != "" && pageURL != "" {
		if base, err := url.Parse(pageURL); err == nil {
			if imgURL, err := url.Parse(m.ImageURL); err == nil {
				m.ImageURL = base.ResolveReference(imgURL).String()
			}
		}
		if !strings.HasPrefix(m.ImageURL, "http://") && !strings.HasPrefix(m.ImageURL, "https://") {
			m.ImageURL = ""
		}
	}

	return m
}

// Fetch retrieves and extracts OpenGraph metadata from a given webpage URL.
func Fetch(ctx context.Context, client *http.Client, pageURL string) (*Metadata, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	// Read up to 512KB for head/meta tags.
	lr := &io.LimitedReader{R: resp.Body, N: 512 << 10}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}

	return Extract(body, pageURL), nil
}

// FetchImage downloads image bytes from imageURL, validating image MIME type.
func FetchImage(ctx context.Context, client *http.Client, imageURL string, maxBytes int64) ([]byte, string, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10MB default
	}

	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("image fetch failed with status %d", resp.StatusCode)
	}

	lr := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", errors.New("image exceeds maximum allowed size")
	}

	mimeType := resp.Header.Get("Content-Type")
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
		if idx := strings.Index(mimeType, ";"); idx != -1 {
			mimeType = strings.TrimSpace(mimeType[:idx])
		}
	}

	// Validate supported image format.
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return data, mimeType, nil
	default:
		return nil, "", fmt.Errorf("unsupported image format: %s", mimeType)
	}
}
