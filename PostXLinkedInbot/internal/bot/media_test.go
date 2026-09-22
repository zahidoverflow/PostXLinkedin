package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestDetectMediaCategory(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		mimeType string
		want     MediaCategory
	}{
		// Images
		{"JPEG ext", "pic.jpg", "", MediaCategoryImage},
		{"JPEG upper", "PIC.JPEG", "", MediaCategoryImage},
		{"PNG", "icon.png", "image/png", MediaCategoryImage},
		{"GIF", "anim.gif", "image/gif", MediaCategoryImage},
		{"WEBP", "photo.webp", "image/webp", MediaCategoryImage},
		{"HEIC", "apple.heic", "image/heic", MediaCategoryImage},
		{"HEIF", "apple.heif", "image/heif", MediaCategoryImage},

		// Documents
		{"PDF", "manual.pdf", "application/pdf", MediaCategoryDocument},
		{"DOC", "old.doc", "application/msword", MediaCategoryDocument},
		{"DOCX", "report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", MediaCategoryDocument},
		{"PPT", "slides.ppt", "application/vnd.ms-powerpoint", MediaCategoryDocument},
		{"PPTX", "deck.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", MediaCategoryDocument},
		{"ODT", "doc.odt", "application/vnd.oasis.opendocument.text", MediaCategoryDocument},
		{"ODS", "sheet.ods", "application/vnd.oasis.opendocument.spreadsheet", MediaCategoryDocument},
		{"PPSX", "show.ppsx", "application/vnd.openxmlformats-officedocument.presentationml.slideshow", MediaCategoryDocument},

		// Videos
		{"MP4", "clip.mp4", "video/mp4", MediaCategoryVideo},
		{"MOV", "movie.mov", "video/quicktime", MediaCategoryVideo},
		{"AVI", "video.avi", "video/x-msvideo", MediaCategoryVideo},
		{"WEBM", "clip.webm", "video/webm", MediaCategoryVideo},
		{"MKV", "file.mkv", "video/x-matroska", MediaCategoryVideo},
		{"WMV", "file.wmv", "video/x-ms-wmv", MediaCategoryVideo},
		{"MPEG", "file.mpeg", "video/mpeg", MediaCategoryVideo},

		// Audio
		{"MP3", "song.mp3", "audio/mpeg", MediaCategoryAudio},
		{"WAV", "sound.wav", "audio/wav", MediaCategoryAudio},
		{"FLAC", "track.flac", "audio/flac", MediaCategoryAudio},
		{"AAC", "track.aac", "audio/aac", MediaCategoryAudio},
		{"OGG", "voice.ogg", "audio/ogg", MediaCategoryAudio},

		// Unknown / unsupported
		{"EXE", "app.exe", "application/x-msdownload", MediaCategoryUnknown},
		{"ZIP", "archive.zip", "application/zip", MediaCategoryUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMediaCategory(tt.filename, tt.mimeType)
			if got != tt.want {
				t.Errorf("DetectMediaCategory(%q, %q) = %v, want %v", tt.filename, tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestExtractInboundMedia(t *testing.T) {
	// 1. Photo
	photoMsg := &tgbotapi.Message{
		Photo: []tgbotapi.PhotoSize{
			{FileID: "photo-small", FileSize: 100},
			{FileID: "photo-large", FileSize: 500},
		},
		Caption: "Look at this photo",
	}
	m := ExtractInboundMedia(photoMsg)
	if m == nil || m.Category != MediaCategoryImage || m.FileID != "photo-large" {
		t.Errorf("expected photo-large image, got %+v", m)
	}

	// 2. Document (PDF forwarded from channel)
	docMsg := &tgbotapi.Message{
		ForwardFromChat: &tgbotapi.Chat{ID: -1001234567, Title: "Tech News"},
		ForwardDate:     1700000000,
		Document: &tgbotapi.Document{
			FileID:   "doc-pdf-123",
			FileName: "Quarterly_Report.pdf",
			MimeType: "application/pdf",
			FileSize: 204800,
		},
		Caption: "Q3 Results #finance",
	}
	m = ExtractInboundMedia(docMsg)
	if m == nil || m.Category != MediaCategoryDocument || m.Filename != "Quarterly_Report.pdf" {
		t.Errorf("expected PDF document, got %+v", m)
	}

	// 3. Video (Forwarded)
	videoMsg := &tgbotapi.Message{
		ForwardFrom: &tgbotapi.User{ID: 111, UserName: "alice"},
		ForwardDate: 1700000000,
		Video: &tgbotapi.Video{
			FileID:   "vid-456",
			FileName: "demo.mp4",
			MimeType: "video/mp4",
			FileSize: 1048576,
		},
		Caption: "Product walkthrough",
	}
	m = ExtractInboundMedia(videoMsg)
	if m == nil || m.Category != MediaCategoryVideo || m.FileID != "vid-456" {
		t.Errorf("expected MP4 video, got %+v", m)
	}

	// 4. VideoNote
	vnMsg := &tgbotapi.Message{
		VideoNote: &tgbotapi.VideoNote{
			FileID:   "vn-789",
			FileSize: 50000,
		},
	}
	m = ExtractInboundMedia(vnMsg)
	if m == nil || m.Category != MediaCategoryVideo {
		t.Errorf("expected VideoNote as video, got %+v", m)
	}

	// 5. Animation (GIF)
	animMsg := &tgbotapi.Message{
		Animation: &tgbotapi.Animation{
			FileID:   "anim-1",
			FileName: "fun.gif",
			MimeType: "image/gif",
			FileSize: 30000,
		},
		Caption: "Happy Friday!",
	}
	m = ExtractInboundMedia(animMsg)
	if m == nil || m.Category != MediaCategoryImage {
		t.Errorf("expected animation as image, got %+v", m)
	}

	// 6. Audio
	audioMsg := &tgbotapi.Message{
		Audio: &tgbotapi.Audio{
			FileID:   "audio-1",
			FileName: "podcast.mp3",
			MimeType: "audio/mpeg",
			FileSize: 100000,
		},
		Caption: "Episode 1",
	}
	m = ExtractInboundMedia(audioMsg)
	if m == nil || m.Category != MediaCategoryAudio {
		t.Errorf("expected audio, got %+v", m)
	}

	// 7. Text-only message
	textMsg := &tgbotapi.Message{
		Text: "Just a plain tweet/post",
	}
	m = ExtractInboundMedia(textMsg)
	if m != nil {
		t.Errorf("expected nil media for text message, got %+v", m)
	}
}

func TestExtractFirstURL(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []tgbotapi.MessageEntity
		want     string
	}{
		{
			name: "plain text with https url and punctuation",
			text: "Check out this project: https://github.com/zahidoverflow/postxlinkedin.",
			want: "https://github.com/zahidoverflow/postxlinkedin",
		},
		{
			name: "plain text with http url in brackets",
			text: "Link here: (http://example.com/page?ref=1&b=2)",
			want: "http://example.com/page?ref=1&b=2",
		},
		{
			name: "message with telegram url entity",
			text: "Visit https://news.ycombinator.com today",
			entities: []tgbotapi.MessageEntity{
				{
					Type:   "url",
					Offset: 6,
					Length: 28,
				},
			},
			want: "https://news.ycombinator.com",
		},
		{
			name: "message with telegram text_link entity",
			text: "Read the full announcement here",
			entities: []tgbotapi.MessageEntity{
				{
					Type:   "text_link",
					Offset: 24,
					Length: 4,
					URL:    "https://blog.example.com/announcement",
				},
			},
			want: "https://blog.example.com/announcement",
		},
		{
			name: "text without any links",
			text: "Just an ordinary status update without links",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFirstURL(tt.text, tt.entities)
			if got != tt.want {
				t.Errorf("ExtractFirstURL(%q, entities) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
