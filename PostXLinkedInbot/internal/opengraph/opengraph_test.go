package opengraph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtract(t *testing.T) {
	htmlSample := `
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Fallback Title</title>
    <meta property="og:title" content="Real OG Title &amp; More" />
    <meta property="og:description" content="This is an awesome description." />
    <meta property="og:image" content="/images/preview.png" />
    <meta name="twitter:card" content="summary_large_image" />
</head>
<body>
    <h1>Hello World</h1>
</body>
</html>
`
	meta := Extract([]byte(htmlSample), "https://example.com/blog/article-1")
	if meta.Title != "Real OG Title & More" {
		t.Errorf("expected 'Real OG Title & More', got %q", meta.Title)
	}
	if meta.Description != "This is an awesome description." {
		t.Errorf("expected 'This is an awesome description.', got %q", meta.Description)
	}
	if meta.ImageURL != "https://example.com/images/preview.png" {
		t.Errorf("expected 'https://example.com/images/preview.png', got %q", meta.ImageURL)
	}
}

func TestExtractReversedAttributesAndTwitterFallback(t *testing.T) {
	htmlSample := `
<!DOCTYPE html>
<html>
<head>
    <meta content='Twitter Card Title' name='twitter:title'>
    <meta content='Twitter Card Description' name='twitter:description'>
    <meta content='https://cdn.example.com/photo.jpg' name='twitter:image'>
</head>
<body></body>
</html>
`
	meta := Extract([]byte(htmlSample), "https://example.com")
	if meta.Title != "Twitter Card Title" {
		t.Errorf("expected 'Twitter Card Title', got %q", meta.Title)
	}
	if meta.Description != "Twitter Card Description" {
		t.Errorf("expected 'Twitter Card Description', got %q", meta.Description)
	}
	if meta.ImageURL != "https://cdn.example.com/photo.jpg" {
		t.Errorf("expected 'https://cdn.example.com/photo.jpg', got %q", meta.ImageURL)
	}
}

func TestExtractFallbackToTitleTag(t *testing.T) {
	htmlSample := `
<!DOCTYPE html>
<html>
<head>
    <title>Simple Page Title</title>
    <meta name="description" content="Meta standard description" />
</head>
<body></body>
</html>
`
	meta := Extract([]byte(htmlSample), "https://example.com")
	if meta.Title != "Simple Page Title" {
		t.Errorf("expected 'Simple Page Title', got %q", meta.Title)
	}
	if meta.Description != "Meta standard description" {
		t.Errorf("expected 'Meta standard description', got %q", meta.Description)
	}
	if meta.ImageURL != "" {
		t.Errorf("expected empty ImageURL, got %q", meta.ImageURL)
	}
}

func TestFetchAndFetchImage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/article", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<html>
			<head>
				<meta property="og:title" content="Server Article" />
				<meta property="og:image" content="/img.png" />
			</head>
			</html>
		`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// 1x1 transparent PNG bytes
		pngBytes := []byte{
			0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
			0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
			0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
			0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
			0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
		}
		_, _ = w.Write(pngBytes)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	meta, err := Fetch(ctx, srv.Client(), srv.URL+"/article")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if meta.Title != "Server Article" {
		t.Errorf("expected 'Server Article', got %q", meta.Title)
	}
	if meta.ImageURL != srv.URL+"/img.png" {
		t.Errorf("expected %q, got %q", srv.URL+"/img.png", meta.ImageURL)
	}

	imgData, mimeType, err := FetchImage(ctx, srv.Client(), meta.ImageURL, 1<<20)
	if err != nil {
		t.Fatalf("FetchImage failed: %v", err)
	}
	if mimeType != "image/png" {
		t.Errorf("expected image/png, got %s", mimeType)
	}
	if len(imgData) == 0 {
		t.Errorf("expected non-empty image data")
	}
}
