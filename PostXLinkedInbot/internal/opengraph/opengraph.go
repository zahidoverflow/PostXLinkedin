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
	"strconv"
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
	metaTagRegex        = regexp.MustCompile(`(?is)<meta\s+([^>]+)>`)
	linkTagRegex        = regexp.MustCompile(`(?is)<link\s+([^>]+)>`)
	imgTagRegex         = regexp.MustCompile(`(?is)<img\s+([^>]+)>`)
	attrRegex           = regexp.MustCompile(`(?i)([a-z0-9_\-:]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	titleRegex          = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	articleBlockRegex   = regexp.MustCompile(`(?is)<article\b[^>]*>.*?</article>`)
	mainBlockRegex      = regexp.MustCompile(`(?is)<main\b[^>]*>.*?</main>`)
	contentDivRegex     = regexp.MustCompile(`(?is)<div\b[^>]*class\s*=\s*["'][^"']*(?:entry-content|post-content|article-content|post-body|story-body)[^"']*["'][^>]*>`)
	jsonLDImageRegex    = regexp.MustCompile(`(?is)"(?:image|thumbnailUrl)"\s*:\s*(?:\{\s*"[^"]*"\s*:\s*"[^"]*"\s*,\s*"url"\s*:\s*"(https?://[^"]+)"|"(https?://[^"]+)")`)
	tgPreviewImageRegex = regexp.MustCompile(`(?is)class="[^"]*link_preview_(?:right_)?image[^"]*"[^>]+style="[^"]*background-image:url\('([^']+)'\)`)
)

const (
	defaultUserAgent     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
	telegramBotUserAgent = "TelegramBot (like TwitterBot)"
)

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
		if key == "" {
			key = strings.ToLower(attrs["itemprop"])
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
		case "og:image", "og:image:url", "og:image:secure_url", "twitter:image", "twitter:image:src", "image", "thumbnail":
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

	if m.ImageURL == "" {
		m.ImageURL = extractLinkRelImage(text)
	}
	if m.ImageURL == "" {
		m.ImageURL = extractJSONLDImage(text)
	}
	if m.ImageURL == "" {
		m.ImageURL = extractFirstArticleImage(text)
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

func parseTagAttrs(rawAttrs string) map[string]string {
	attrPairs := attrRegex.FindAllStringSubmatch(rawAttrs, -1)
	attrs := make(map[string]string, len(attrPairs))
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
	return attrs
}

func extractLinkRelImage(text string) string {
	for _, match := range linkTagRegex.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		attrs := parseTagAttrs(match[1])
		if strings.EqualFold(attrs["rel"], "image_src") && attrs["href"] != "" {
			return attrs["href"]
		}
	}
	return ""
}

func extractJSONLDImage(text string) string {
	if m := jsonLDImageRegex.FindStringSubmatch(text); len(m) > 1 {
		for _, g := range m[1:] {
			if g != "" && !strings.HasSuffix(strings.ToLower(g), ".svg") {
				return strings.ReplaceAll(g, `\/`, `/`)
			}
		}
	}
	return ""
}

func extractFirstArticleImage(text string) string {
	var regions []string
	if m := articleBlockRegex.FindString(text); m != "" {
		regions = append(regions, m)
	}
	if m := mainBlockRegex.FindString(text); m != "" {
		regions = append(regions, m)
	}
	if loc := contentDivRegex.FindStringIndex(text); loc != nil {
		end := loc[0] + 100000
		if end > len(text) {
			end = len(text)
		}
		regions = append(regions, text[loc[0]:end])
	}

	for _, region := range regions {
		if img := findFirstContentImg(region); img != "" {
			return img
		}
	}
	return ""
}

func findFirstContentImg(region string) string {
	for _, match := range imgTagRegex.FindAllStringSubmatch(region, -1) {
		if len(match) < 2 {
			continue
		}
		attrs := parseTagAttrs(match[1])
		src := strings.TrimSpace(html.UnescapeString(attrs["src"]))
		if src == "" || strings.HasPrefix(strings.ToLower(src), "data:") {
			for _, k := range []string{"data-src", "data-lazy-src", "data-original"} {
				if v := strings.TrimSpace(html.UnescapeString(attrs[k])); v != "" && !strings.HasPrefix(strings.ToLower(v), "data:") {
					src = v
					break
				}
			}
		}
		if src == "" || strings.HasPrefix(strings.ToLower(src), "data:") || strings.HasPrefix(strings.ToLower(src), "javascript:") {
			continue
		}

		lowerSrc := strings.ToLower(src)
		lowerClass := strings.ToLower(attrs["class"] + " " + attrs["id"])
		if strings.HasSuffix(lowerSrc, ".svg") || strings.HasSuffix(lowerSrc, ".ico") {
			continue
		}
		if strings.Contains(lowerSrc, "avatar") || strings.Contains(lowerSrc, "gravatar") ||
			strings.Contains(lowerSrc, "emoji") || strings.Contains(lowerSrc, "smilies") ||
			strings.Contains(lowerSrc, "1x1") || strings.Contains(lowerSrc, "pixel") ||
			strings.Contains(lowerSrc, "spacer") ||
			strings.Contains(lowerClass, "avatar") || strings.Contains(lowerClass, "wp-smiley") ||
			strings.Contains(lowerClass, "emoji") {
			continue
		}

		if w, err := strconv.Atoi(attrs["width"]); err == nil && w > 0 && w < 120 {
			continue
		}
		if h, err := strconv.Atoi(attrs["height"]); err == nil && h > 0 && h < 120 {
			continue
		}

		return src
	}
	return ""
}

// Fetch retrieves and extracts OpenGraph metadata from a given webpage URL.
// It tries Telegram's link-preview User-Agent first and falls back to a standard browser User-Agent
// if the initial request fails or yields no preview image.
func Fetch(ctx context.Context, client *http.Client, pageURL string) (*Metadata, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	userAgents := []string{telegramBotUserAgent, defaultUserAgent}
	var bestMeta *Metadata
	var lastErr error

	for _, ua := range userAgents {
		req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			resp.Body.Close()
			lastErr = fmt.Errorf("http status %d", resp.StatusCode)
			continue
		}

		ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
		if strings.HasPrefix(ct, "image/") {
			resp.Body.Close()
			return &Metadata{ImageURL: pageURL}, nil
		}

		// Read up to 512KB for head/meta tags.
		lr := &io.LimitedReader{R: resp.Body, N: 512 << 10}
		body, err := io.ReadAll(lr)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		meta := Extract(body, pageURL)
		if bestMeta == nil {
			bestMeta = meta
		} else {
			if bestMeta.Title == "" && meta.Title != "" {
				bestMeta.Title = meta.Title
			}
			if bestMeta.Description == "" && meta.Description != "" {
				bestMeta.Description = meta.Description
			}
			if bestMeta.ImageURL == "" && meta.ImageURL != "" {
				bestMeta.ImageURL = meta.ImageURL
			}
		}

		if bestMeta.ImageURL != "" {
			return bestMeta, nil
		}
	}

	if bestMeta != nil {
		return bestMeta, nil
	}
	return nil, lastErr
}

// FetchTelegramWebPreviewImage extracts the cached link-preview image URL from a public Telegram channel post embed.
func FetchTelegramWebPreviewImage(ctx context.Context, client *http.Client, channelUsername string, messageID int) string {
	channelUsername = strings.TrimSpace(strings.TrimPrefix(channelUsername, "@"))
	if channelUsername == "" || messageID <= 0 {
		return ""
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	embedURL := fmt.Sprintf("https://t.me/%s/%d?embed=1", channelUsername, messageID)
	req, err := http.NewRequestWithContext(ctx, "GET", embedURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}

	lr := &io.LimitedReader{R: resp.Body, N: 256 << 10}
	body, err := io.ReadAll(lr)
	if err != nil {
		return ""
	}

	if m := tgPreviewImageRegex.FindStringSubmatch(string(body)); len(m) > 1 {
		img := strings.TrimSpace(html.UnescapeString(m[1]))
		if strings.HasPrefix(img, "//") {
			img = "https:" + img
		}
		if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") {
			return img
		}
	}
	return ""
}

// FetchImage downloads image bytes from imageURL, validating image MIME type.
// Includes automatic retries with backoff for transient rate limits (e.g. HTTP 429 on dynamic OG renderers).
func FetchImage(ctx context.Context, client *http.Client, imageURL string, maxBytes int64) ([]byte, string, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10MB default
	}

	userAgents := []string{defaultUserAgent, telegramBotUserAgent, defaultUserAgent}
	var lastErr error

	for attempt, ua := range userAgents {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(time.Duration(attempt*600) * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 || resp.StatusCode == http.StatusAccepted {
			resp.Body.Close()
			lastErr = fmt.Errorf("image fetch failed with status %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, "", fmt.Errorf("image fetch failed with status %d", resp.StatusCode)
		}

		lr := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
		data, err := io.ReadAll(lr)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
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

	return nil, "", lastErr
}
