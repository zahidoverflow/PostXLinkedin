package bot

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"

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
		{
			name: "multiple urls picks first url before later text_link",
			text: "Check https://github.com/nullfuzz-pentest/shodan-dorks and also Source.",
			entities: []tgbotapi.MessageEntity{
				{
					Type:   "url",
					Offset: 6,
					Length: 48,
				},
				{
					Type:   "text_link",
					Offset: 64,
					Length: 7,
					URL:    "https://second.example.com/article",
				},
			},
			want: "https://github.com/nullfuzz-pentest/shodan-dorks",
		},
		{
			name: "multiple urls picks first text_link before later plain url",
			text: "First (https://first.example.com/one) and second https://second.example.com/two",
			want: "https://first.example.com/one",
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

func TestFormatPostURLs(t *testing.T) {
	// LinkedIn post URLs
	if got := FormatLinkedInPostURL("urn:li:ugcPost:7484167400423071744"); got != "https://www.linkedin.com/feed/update/urn:li:ugcPost:7484167400423071744/" {
		t.Errorf("unexpected linkedin URL: %s", got)
	}
	if got := FormatLinkedInPostURL("urn:li:share:123456/"); got != "https://www.linkedin.com/feed/update/urn:li:share:123456/" {
		t.Errorf("unexpected linkedin URL: %s", got)
	}
	if got := FormatLinkedInPostURL("99999999"); got != "https://www.linkedin.com/feed/update/urn:li:activity:99999999/" {
		t.Errorf("unexpected linkedin URL: %s", got)
	}
	if got := FormatLinkedInPostURL(""); got != "" {
		t.Errorf("expected empty for empty post ID, got %s", got)
	}
	if got := FormatLinkedInPostURL("ok"); got != "" {
		t.Errorf("expected empty for 'ok', got %s", got)
	}

	// X post URLs
	if got := FormatXPostURL("1771234567890"); got != "https://x.com/i/status/1771234567890" {
		t.Errorf("unexpected X URL: %s", got)
	}
	if got := FormatXPostURL(""); got != "" {
		t.Errorf("expected empty for empty tweet ID, got %s", got)
	}
	if got := FormatXPostURL("ok"); got != "" {
		t.Errorf("expected empty for 'ok', got %s", got)
	}
}

func TestExpandTextLinks(t *testing.T) {
	// 1. User sample case with emojis and hyperlinked "Source."
	sampleText := "🥺 Sam Altman apparently told OpenAI employees that there are possibly THREE endgames for AI safety:\n\n1. An international nuclear-style watchdog\n\n2. Merge all major labs into a giant org\n\n3. One company achieves RSI and gets so powerful it can literally “melt everyone else’s GPUs.”\n\nand he called the 3rd option “terrible.\n\nSource.\n\n@aipost 🏴"
	sampleURL := "https://www.theinformation.com/articles/altman-describes-three-endgames-ai-including-one-single-firm-melts-everyone-elses-gpus?rc=jm6dbd&shared=dead241b54ae7c75"

	sourceIdx := len([]rune(sampleText[:strings.Index(sampleText, "Source.")]))
	// Calculate UTF-16 offset as Telegram does
	utf16SourceOffset := len(utf16.Encode([]rune(sampleText[:strings.Index(sampleText, "Source.")])))
	utf16SourceLen := len(utf16.Encode([]rune("Source.")))

	entities := []tgbotapi.MessageEntity{
		{
			Type:   "text_link",
			Offset: utf16SourceOffset,
			Length: utf16SourceLen,
			URL:    sampleURL,
		},
		{
			Type:   "mention",
			Offset: len(utf16.Encode([]rune(sampleText[:strings.Index(sampleText, "@aipost")]))),
			Length: len("@aipost"),
		},
	}

	expectedSample := "🥺 Sam Altman apparently told OpenAI employees that there are possibly THREE endgames for AI safety:\n\n1. An international nuclear-style watchdog\n\n2. Merge all major labs into a giant org\n\n3. One company achieves RSI and gets so powerful it can literally “melt everyone else’s GPUs.”\n\nand he called the 3rd option “terrible.\n\nSource. (" + sampleURL + ")\n\n@aipost 🏴"

	gotSample := ExpandTextLinks(sampleText, entities)
	if gotSample != expectedSample {
		t.Errorf("ExpandTextLinks sample mismatch:\nGOT:\n%s\nWANT:\n%s", gotSample, expectedSample)
	}
	_ = sourceIdx

	// 2. Multiple links
	multiText := "Read Part 1 and Part 2 now!"
	p1Offset := len(utf16.Encode([]rune(multiText[:strings.Index(multiText, "Part 1")])))
	p1Len := len("Part 1")
	p2Offset := len(utf16.Encode([]rune(multiText[:strings.Index(multiText, "Part 2")])))
	p2Len := len("Part 2")

	multiEntities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: p2Offset, Length: p2Len, URL: "https://example.com/2"}, // Out of order to test sorting
		{Type: "text_link", Offset: p1Offset, Length: p1Len, URL: "https://example.com/1"},
	}
	wantMulti := "Read Part 1 (https://example.com/1) and Part 2 (https://example.com/2) now!"
	if got := ExpandTextLinks(multiText, multiEntities); got != wantMulti {
		t.Errorf("ExpandTextLinks multiple got %q, want %q", got, wantMulti)
	}

	// 3. Already followed by parenthesized URL (avoid duplication)
	followedText := "Source (https://example.com)"
	followedEntities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: 0, Length: len("Source"), URL: "https://example.com"},
	}
	if got := ExpandTextLinks(followedText, followedEntities); got != followedText {
		t.Errorf("ExpandTextLinks duplicate got %q, want %q", got, followedText)
	}

	// 4. Anchor text is already the URL itself
	urlAnchorText := "Visit https://example.com for more info"
	urlAnchorOffset := strings.Index(urlAnchorText, "https://example.com")
	urlEntities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: urlAnchorOffset, Length: len("https://example.com"), URL: "https://example.com"},
	}
	if got := ExpandTextLinks(urlAnchorText, urlEntities); got != urlAnchorText {
		t.Errorf("ExpandTextLinks url anchor got %q, want %q", got, urlAnchorText)
	}

	// 5. Anchor text with trailing whitespace
	wsText := "Check this link for details."
	wsOffset := strings.Index(wsText, "link ")
	wsEntities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: wsOffset, Length: len("link "), URL: "https://example.com"},
	}
	wantWS := "Check this link (https://example.com) for details."
	if got := ExpandTextLinks(wsText, wsEntities); got != wantWS {
		t.Errorf("ExpandTextLinks whitespace got %q, want %q", got, wantWS)
	}

	// 6. No entities or empty text
	if got := ExpandTextLinks("", multiEntities); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := ExpandTextLinks("plain text", nil); got != "plain text" {
		t.Errorf("expected original text, got %q", got)
	}

	// 7. Invalid offsets
	badEntities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: 999, Length: 10, URL: "https://bad.com"},
		{Type: "text_link", Offset: -5, Length: 10, URL: "https://bad.com"},
	}
	if got := ExpandTextLinks("safe text", badEntities); got != "safe text" {
		t.Errorf("expected safe text for out-of-range offsets, got %q", got)
	}
}

func TestSampleAIPost8557(t *testing.T) {
	// Exact text from https://t.me/aipost/8557
	postCaption := "🥺 Sam Altman apparently told OpenAI employees that there are possibly THREE endgames for AI safety:\n\n1. An international nuclear-style watchdog\n\n2. Merge all major labs into a giant org\n\n3. One company achieves RSI and gets so powerful it can literally “melt everyone else’s GPUs.”\n\nand he called the 3rd option “terrible.\n\nSource.\n\n@aipost 🏴"
	sourceURL := "https://www.theinformation.com/articles/altman-describes-three-endgames-ai-including-one-single-firm-melts-everyone-elses-gpus?rc=jm6dbd&shared=dead241b54ae7c75"

	// Find the exact UTF-16 offset of "Source."
	sourceIdx := strings.Index(postCaption, "Source.")
	if sourceIdx == -1 {
		t.Fatalf("Source. not found in caption")
	}
	u16Prefix := utf16.Encode([]rune(postCaption[:sourceIdx]))
	offset := len(u16Prefix)
	length := len(utf16.Encode([]rune("Source.")))

	mentionIdx := strings.Index(postCaption, "@aipost")
	mentionOffset := len(utf16.Encode([]rune(postCaption[:mentionIdx])))

	entities := []tgbotapi.MessageEntity{
		{Type: "custom_emoji", Offset: 0, Length: 2},
		{Type: "text_link", Offset: offset, Length: length, URL: sourceURL},
		{Type: "mention", Offset: mentionOffset, Length: len("@aipost")},
		{Type: "custom_emoji", Offset: len(utf16.Encode([]rune(postCaption))) - 2, Length: 2},
	}

	expected := "🥺 Sam Altman apparently told OpenAI employees that there are possibly THREE endgames for AI safety:\n\n1. An international nuclear-style watchdog\n\n2. Merge all major labs into a giant org\n\n3. One company achieves RSI and gets so powerful it can literally “melt everyone else’s GPUs.”\n\nand he called the 3rd option “terrible.\n\nSource. (" + sourceURL + ")\n\n@aipost 🏴"

	result := ExpandTextLinks(postCaption, entities)
	if result != expected {
		t.Fatalf("Output does not match expected!\nGOT:\n%s\nWANT:\n%s", result, expected)
	}

	// Also test HTML-escaped URL from Telegram web embed (&amp; instead of &)
	escapedURL := "https://www.theinformation.com/articles/altman-describes-three-endgames-ai-including-one-single-firm-melts-everyone-elses-gpus?rc=jm6dbd&amp;shared=dead241b54ae7c75"
	escapedEntities := []tgbotapi.MessageEntity{
		{Type: "text_link", Offset: offset, Length: length, URL: escapedURL},
	}
	escapedResult := ExpandTextLinks(postCaption, escapedEntities)
	if escapedResult != expected {
		t.Fatalf("Escaped URL output does not match expected!\nGOT:\n%s\nWANT:\n%s", escapedResult, expected)
	}
}

func TestMediaGroupCollector(t *testing.T) {
	ready := make(chan []*tgbotapi.Message, 10)
	collector := newMediaGroupCollector(ready, 100*time.Millisecond)
	defer collector.StopAll()

	msg1 := &tgbotapi.Message{MessageID: 1, MediaGroupID: "group-100", Caption: "First"}
	msg2 := &tgbotapi.Message{MessageID: 2, MediaGroupID: "group-100"}
	msg3 := &tgbotapi.Message{MessageID: 3, MediaGroupID: "group-100"}

	collector.Add(msg1)
	collector.Add(msg2)
	collector.Add(msg3)

	select {
	case batch := <-ready:
		if len(batch) != 3 {
			t.Fatalf("expected 3 messages in batch, got %d", len(batch))
		}
		if batch[0].MessageID != 1 || batch[1].MessageID != 2 || batch[2].MessageID != 3 {
			t.Errorf("unexpected message IDs: %v", batch)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for media group batch")
	}
}

func TestMediaGroupCollectorForwardWithComment(t *testing.T) {
	ready := make(chan []*tgbotapi.Message, 10)
	collector := newMediaGroupCollector(ready, 150*time.Millisecond)
	defer collector.StopAll()

	chat := &tgbotapi.Chat{ID: 593365882}
	// User forwards PDF with comment: Telegram sends comment text first, then forwarded PDF
	commentMsg := &tgbotapi.Message{
		MessageID: 2244,
		Chat:      chat,
		Text:      "818312296-05-Windows-Lateral-Movement.pdf",
	}
	pdfMsg := &tgbotapi.Message{
		MessageID:   2245,
		Chat:        chat,
		ForwardDate: 1792758597,
		Document: &tgbotapi.Document{
			FileID:   "doc-file-id-1",
			FileName: "818312296-05-Windows-Lateral-Movement.pdf",
			MimeType: "application/pdf",
		},
	}

	collector.Add(commentMsg)
	collector.Add(pdfMsg)

	select {
	case batch := <-ready:
		if len(batch) != 2 {
			t.Fatalf("expected 2 messages merged into batch, got %d", len(batch))
		}
		if batch[0].MessageID != 2244 || batch[1].MessageID != 2245 {
			t.Errorf("unexpected message order: %d, %d", batch[0].MessageID, batch[1].MessageID)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for merged comment+PDF batch")
	}

	// Ensure no duplicate timer emission happens after timeout
	select {
	case extra := <-ready:
		t.Fatalf("unexpected extra batch emitted: %+v", extra)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestRedundantFilenameCommentAndMeaningfulFilename(t *testing.T) {
	if !isRedundantFilenameComment(
		"Nmap-From-Basics-to-Pro-pptx.pdf",
		"👁 Nmap Basics to Pro\n\nNmap is one of the most important tools for OSCP Exam.",
		[]string{"1053577189-Nmap-From-Basics-to-Pro-pptx.pdf"},
	) {
		t.Errorf("expected Nmap-From-Basics-to-Pro-pptx.pdf to be recognized as redundant filename comment")
	}

	if !isRedundantFilenameComment(
		"bountytips100+.pdf",
		"Some caption",
		[]string{"bounty_tips_100+.pdf"},
	) {
		t.Errorf("expected bountytips100+.pdf to match bounty_tips_100+.pdf")
	}

	if isRedundantFilenameComment(
		"Must-read OSCP notes for lateral movement!",
		"👁 Nmap Basics to Pro",
		[]string{"1053577189-Nmap-From-Basics-to-Pro-pptx.pdf"},
	) {
		t.Errorf("expected genuine user commentary not to be treated as redundant filename")
	}

	if !hasMeaningfulFilename("bounty_tips_100+.pdf") {
		t.Errorf("expected bounty_tips_100+.pdf to be meaningful filename")
	}
	if hasMeaningfulFilename("photo.jpg") {
		t.Errorf("expected photo.jpg not to be meaningful filename")
	}
}
