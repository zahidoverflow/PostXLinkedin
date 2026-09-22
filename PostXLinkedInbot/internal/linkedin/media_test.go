package linkedin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLinkedInDocumentUploadAndPost(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/rest/documents", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "initializeUpload" {
			t.Errorf("unexpected action: %s", r.URL.Query().Get("action"))
		}
		resp := initDocUploadResp{}
		resp.Value.UploadURL = "http://" + r.Host + "/upload-doc"
		resp.Value.Document = "urn:li:document:12345"
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/upload-doc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/rest/posts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("x-restli-id", "urn:li:share:99999")
		w.WriteHeader(http.StatusCreated)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.Client(), "test-token", "202601")
	client.SetBaseURL(srv.URL)

	ctx := context.Background()
	uploadURL, docURN, err := client.InitializeDocumentUpload(ctx, "urn:li:person:zahid")
	if err != nil {
		t.Fatalf("InitializeDocumentUpload failed: %v", err)
	}
	if docURN != "urn:li:document:12345" {
		t.Fatalf("expected urn:li:document:12345, got %s", docURN)
	}

	if err := client.UploadDocumentBytes(ctx, uploadURL, "application/pdf", []byte("pdf-data")); err != nil {
		t.Fatalf("UploadDocumentBytes failed: %v", err)
	}

	postID, err := client.CreateDocumentPost(ctx, "urn:li:person:zahid", "Check out this doc!", docURN, "Report.pdf")
	if err != nil {
		t.Fatalf("CreateDocumentPost failed: %v", err)
	}
	if postID != "urn:li:share:99999" {
		t.Fatalf("expected urn:li:share:99999, got %s", postID)
	}
}

func TestLinkedInVideoUploadAndPost(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/rest/videos", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("action")
		switch action {
		case "initializeUpload":
			resp := initVideoUploadResp{}
			resp.Value.Video = "urn:li:video:777"
			resp.Value.UploadToken = "token-abc"
			resp.Value.UploadInstructions = []VideoUploadInstruction{
				{
					UploadURL: "http://" + r.Host + "/upload-video-part",
					FirstByte: 0,
					LastByte:  9,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "finalizeUpload":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected video action: %s", action)
		}
	})

	mux.HandleFunc("/upload-video-part", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.Header().Set("ETag", "etag-part-0")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/rest/posts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-restli-id", "urn:li:share:video-post-1")
		w.WriteHeader(http.StatusCreated)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.Client(), "test-token", "202601")
	client.SetBaseURL(srv.URL)

	ctx := context.Background()
	videoData := []byte("0123456789")
	videoURN, uploadToken, instructions, err := client.InitializeVideoUpload(ctx, "urn:li:person:zahid", int64(len(videoData)))
	if err != nil {
		t.Fatalf("InitializeVideoUpload failed: %v", err)
	}
	if videoURN != "urn:li:video:777" {
		t.Fatalf("expected urn:li:video:777, got %s", videoURN)
	}

	etags, err := client.UploadVideoParts(ctx, instructions, videoData)
	if err != nil {
		t.Fatalf("UploadVideoParts failed: %v", err)
	}
	if len(etags) != 1 || etags[0] != "etag-part-0" {
		t.Fatalf("unexpected etags: %v", etags)
	}

	if err := client.FinalizeVideoUpload(ctx, videoURN, uploadToken, etags); err != nil {
		t.Fatalf("FinalizeVideoUpload failed: %v", err)
	}

	postID, err := client.CreateVideoPost(ctx, "urn:li:person:zahid", "My new video", videoURN, "clip.mp4")
	if err != nil {
		t.Fatalf("CreateVideoPost failed: %v", err)
	}
	if postID != "urn:li:share:video-post-1" {
		t.Fatalf("expected urn:li:share:video-post-1, got %s", postID)
	}
}

func TestLinkedInArticlePost(t *testing.T) {
	mux := http.NewServeMux()

	var receivedBody map[string]any
	mux.HandleFunc("/rest/posts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		w.Header().Set("x-restli-id", "urn:li:share:article-post-456")
		w.WriteHeader(http.StatusCreated)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(srv.Client(), "test-token", "202601")
	client.SetBaseURL(srv.URL)

	ctx := context.Background()
	postID, err := client.CreateArticlePost(ctx, "urn:li:person:zahid", "Check out this link!", "https://example.com/blog", "", "")
	if err != nil {
		t.Fatalf("CreateArticlePost failed: %v", err)
	}
	if postID != "urn:li:share:article-post-456" {
		t.Fatalf("expected urn:li:share:article-post-456, got %s", postID)
	}

	content, ok := receivedBody["content"].(map[string]any)
	if !ok {
		t.Fatalf("missing content in post payload: %+v", receivedBody)
	}
	article, ok := content["article"].(map[string]any)
	if !ok {
		t.Fatalf("missing article in content: %+v", content)
	}
	if article["source"] != "https://example.com/blog" {
		t.Errorf("expected source https://example.com/blog, got %v", article["source"])
	}
}
