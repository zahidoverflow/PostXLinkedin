package bot

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/telegram"
)

type MediaCategory int

const (
	MediaCategoryUnknown MediaCategory = iota
	MediaCategoryImage
	MediaCategoryDocument
	MediaCategoryVideo
	MediaCategoryAudio
)

func (c MediaCategory) String() string {
	switch c {
	case MediaCategoryImage:
		return "image"
	case MediaCategoryDocument:
		return "document"
	case MediaCategoryVideo:
		return "video"
	case MediaCategoryAudio:
		return "audio"
	default:
		return "unknown"
	}
}

type InboundMedia struct {
	FileID   string
	Filename string
	MIME     string
	FileSize int64
	Category MediaCategory
}

// DetectMediaCategory inspects the filename and MIME type against the official
// LinkedIn supported media file types (https://www.linkedin.com/help/linkedin/answer/a564109).
func DetectMediaCategory(filename, mimeType string) MediaCategory {
	ext := strings.ToLower(filepath.Ext(filename))
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))

	// 1. Check Images: GIF, HEIF/HEIC, JPEG, PNG, WEBP
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heif", ".heic", ".bmp", ".tiff":
		return MediaCategoryImage
	}
	if strings.HasPrefix(mimeType, "image/") {
		return MediaCategoryImage
	}

	// 2. Check Documents: DOC, DOCX, ODT, PPT, PPTX, PDF, PPSX, ODS
	switch ext {
	case ".pdf", ".doc", ".docx", ".odt", ".ppt", ".pptx", ".ppsx", ".ods":
		return MediaCategoryDocument
	}
	switch mimeType {
	case "application/pdf",
		"application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.oasis.opendocument.text",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.openxmlformats-officedocument.presentationml.slideshow",
		"application/vnd.oasis.opendocument.spreadsheet":
		return MediaCategoryDocument
	}

	// 3. Check Videos: MP4, MOV, AVI, WEBM, MKV, WMV, VC1, MPEG, MPEG2, MPEG1VIDEO, DVVIDEO, QTRLE, TSCC2
	switch ext {
	case ".mp4", ".mov", ".avi", ".webm", ".mkv", ".wmv", ".vc1", ".mpeg", ".mpg", ".m1v", ".m2v", ".dv", ".tscc":
		return MediaCategoryVideo
	}
	if strings.HasPrefix(mimeType, "video/") {
		return MediaCategoryVideo
	}

	// 4. Check Audio: AAC, MP3, MP2, ADPCM, ALAC, AMR_NB, FLAC, WAV, WMAV1, WMAV2, WMAVOICE, OPUS, PCM, VORBIS, OGG
	switch ext {
	case ".aac", ".mp3", ".mp2", ".adpcm", ".alac", ".amr", ".flac", ".wav", ".wmav1", ".wmav2", ".wma", ".opus", ".pcm", ".vorbis", ".ogg", ".oga", ".m4a":
		return MediaCategoryAudio
	}
	if strings.HasPrefix(mimeType, "audio/") {
		return MediaCategoryAudio
	}

	return MediaCategoryUnknown
}

// ExtractInboundMedia checks if a Telegram message contains media and extracts its metadata.
func ExtractInboundMedia(msg *tgbotapi.Message) *InboundMedia {
	if len(msg.Photo) > 0 {
		best := telegram.BestPhoto(msg.Photo)
		return &InboundMedia{
			FileID:   best.FileID,
			Filename: "photo.jpg",
			MIME:     "image/jpeg",
			FileSize: int64(best.FileSize),
			Category: MediaCategoryImage,
		}
	}
	if msg.Animation != nil {
		fn := msg.Animation.FileName
		if fn == "" {
			fn = "animation.gif"
		}
		mime := msg.Animation.MimeType
		if mime == "" {
			mime = "image/gif"
		}
		cat := DetectMediaCategory(fn, mime)
		if cat == MediaCategoryUnknown {
			cat = MediaCategoryImage
		}
		return &InboundMedia{
			FileID:   msg.Animation.FileID,
			Filename: fn,
			MIME:     mime,
			FileSize: int64(msg.Animation.FileSize),
			Category: cat,
		}
	}
	if msg.Video != nil {
		fn := msg.Video.FileName
		if fn == "" {
			fn = "video.mp4"
		}
		mime := msg.Video.MimeType
		if mime == "" {
			mime = "video/mp4"
		}
		cat := DetectMediaCategory(fn, mime)
		if cat == MediaCategoryUnknown {
			cat = MediaCategoryVideo
		}
		return &InboundMedia{
			FileID:   msg.Video.FileID,
			Filename: fn,
			MIME:     mime,
			FileSize: int64(msg.Video.FileSize),
			Category: cat,
		}
	}
	if msg.VideoNote != nil {
		return &InboundMedia{
			FileID:   msg.VideoNote.FileID,
			Filename: "videonote.mp4",
			MIME:     "video/mp4",
			FileSize: int64(msg.VideoNote.FileSize),
			Category: MediaCategoryVideo,
		}
	}
	if msg.Audio != nil {
		fn := msg.Audio.FileName
		if fn == "" {
			fn = "audio.mp3"
		}
		mime := msg.Audio.MimeType
		if mime == "" {
			mime = "audio/mpeg"
		}
		return &InboundMedia{
			FileID:   msg.Audio.FileID,
			Filename: fn,
			MIME:     mime,
			FileSize: int64(msg.Audio.FileSize),
			Category: MediaCategoryAudio,
		}
	}
	if msg.Document != nil {
		fn := msg.Document.FileName
		mime := msg.Document.MimeType
		cat := DetectMediaCategory(fn, mime)
		return &InboundMedia{
			FileID:   msg.Document.FileID,
			Filename: fn,
			MIME:     mime,
			FileSize: int64(msg.Document.FileSize),
			Category: cat,
		}
	}
	return nil
}

var urlRegex = regexp.MustCompile(`https?://[^\s<>"'{}|\\^` + "`" + `]+`)

// ExtractFirstURL extracts the first valid HTTP or HTTPS URL from a message
// using Telegram entities if available, falling back to regex.
func ExtractFirstURL(text string, entities []tgbotapi.MessageEntity) string {
	u16 := utf16.Encode([]rune(text))
	for _, ent := range entities {
		if ent.Type == "text_link" && ent.URL != "" {
			return ent.URL
		}
		if ent.Type == "url" {
			start := ent.Offset
			end := ent.Offset + ent.Length
			if start >= 0 && end <= len(u16) && start < end {
				u := string(utf16.Decode(u16[start:end]))
				if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					return strings.TrimRight(u, ".,;:!?)>]}'")
				}
			}
		}
	}

	u := urlRegex.FindString(text)
	if u != "" {
		return strings.TrimRight(u, ".,;:!?)>]}'")
	}
	return ""
}
