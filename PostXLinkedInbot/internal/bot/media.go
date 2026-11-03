package bot

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
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

var urlRegex = regexp.MustCompile(`https?://[^\s<>"'{}|\\^` + "`" + `)\]）]+`)

func cleanExtractedURL(u string) string {
	u = strings.TrimSpace(strings.ReplaceAll(u, "&amp;", "&"))
	return strings.TrimRight(u, ".,;:!?)>]}'\"”）")
}

// isInvisibleAnchor reports whether s contains no visible characters
// (e.g. only whitespace, control chars, or zero-width/format chars like \u200b used by Telegram channels for hidden preview links).
func isInvisibleAnchor(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cc, r) {
			continue
		}
		if r == '\u2800' || r == '\u3164' || r == '\uffa0' || r == '\u180e' {
			continue
		}
		return false
	}
	return true
}

// stripZeroWidth removes zero-width/format characters while preserving normal whitespace.
func stripZeroWidth(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) || r == '\u2800' || r == '\u3164' || r == '\uffa0' || r == '\u180e' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// textContainsPlainURL reports whether targetURL already appears as a plain HTTP/HTTPS URL in text.
func textContainsPlainURL(text, targetURL string) bool {
	targetNorm := strings.TrimRight(cleanExtractedURL(targetURL), "/")
	if targetNorm == "" {
		return false
	}
	for _, m := range urlRegex.FindAllString(text, -1) {
		cand := strings.TrimRight(cleanExtractedURL(m), "/")
		if strings.EqualFold(cand, targetNorm) {
			return true
		}
	}
	return false
}

// ExtractFirstURL extracts the first valid HTTP or HTTPS URL in order of appearance
// from a message, taking into account both plain URLs and Telegram text_link/url entities.
func ExtractFirstURL(text string, entities []tgbotapi.MessageEntity) string {
	bestOffset := -1
	bestURL := ""

	if loc := urlRegex.FindStringIndex(text); loc != nil {
		u := cleanExtractedURL(text[loc[0]:loc[1]])
		if u != "" {
			bestOffset = len(utf16.Encode([]rune(text[:loc[0]])))
			bestURL = u
		}
	}

	u16 := utf16.Encode([]rune(text))
	for _, ent := range entities {
		if ent.Type == "text_link" && strings.TrimSpace(ent.URL) != "" {
			u := cleanExtractedURL(ent.URL)
			if u != "" && (bestOffset == -1 || ent.Offset < bestOffset) {
				bestOffset = ent.Offset
				bestURL = u
			}
		} else if ent.Type == "url" {
			start := ent.Offset
			end := ent.Offset + ent.Length
			if start >= 0 && end <= len(u16) && start < end {
				raw := string(utf16.Decode(u16[start:end]))
				if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
					u := cleanExtractedURL(raw)
					if u != "" && (bestOffset == -1 || start < bestOffset) {
						bestOffset = start
						bestURL = u
					}
				}
			}
		}
	}

	return bestURL
}

// ExpandTextLinks expands Telegram text_link entities into "Anchor (URL)" format.
// If the anchor text is invisible (e.g. \u200b hidden preview link), already identical to the URL,
// immediately followed by the URL in parentheses, or the URL already appears in the plain text,
// duplicate URL insertion is avoided.
func ExpandTextLinks(text string, entities []tgbotapi.MessageEntity) string {
	if text == "" {
		return text
	}
	if len(entities) == 0 {
		return strings.TrimLeft(text, "\u200b\u200c\u200d\u2060\ufeff")
	}

	type textLink struct {
		offset int
		length int
		url    string
	}

	u16 := utf16.Encode([]rune(text))
	totalLen := len(u16)

	var links []textLink
	for _, ent := range entities {
		if ent.Type == "text_link" && strings.TrimSpace(ent.URL) != "" {
			start := ent.Offset
			end := ent.Offset + ent.Length
			if start >= 0 && end <= totalLen && start < end {
				links = append(links, textLink{
					offset: start,
					length: ent.Length,
					url:    strings.TrimSpace(ent.URL),
				})
			}
		}
	}

	if len(links) == 0 {
		return strings.TrimLeft(text, "\u200b\u200c\u200d\u2060\ufeff")
	}

	// Sort by offset ascending; if offsets match, wider range first.
	sort.Slice(links, func(i, j int) bool {
		if links[i].offset == links[j].offset {
			return links[i].length > links[j].length
		}
		return links[i].offset < links[j].offset
	})

	var b strings.Builder
	lastPos := 0

	for _, l := range links {
		start := l.offset
		end := l.offset + l.length

		// Skip overlapping links that start before our current position.
		if start < lastPos {
			continue
		}

		// Append text between previous entity and current entity.
		if start > lastPos {
			b.WriteString(string(utf16.Decode(u16[lastPos:start])))
		}

		anchor := string(utf16.Decode(u16[start:end]))
		cleanURL := strings.ReplaceAll(l.url, "&amp;", "&")

		if isInvisibleAnchor(anchor) {
			b.WriteString(stripZeroWidth(anchor))
			lastPos = end
			continue
		}

		// Check if anchor is already identical to the URL.
		anchorTrimmed := strings.TrimSpace(anchor)
		isSameURL := strings.EqualFold(anchorTrimmed, cleanURL) ||
			strings.EqualFold(strings.TrimRight(anchorTrimmed, "/"), strings.TrimRight(cleanURL, "/"))

		// Check if the text immediately following already has the URL in parentheses.
		alreadyFollowed := false
		if end < totalLen {
			followingText := string(utf16.Decode(u16[end:]))
			trimmedFollowing := strings.TrimLeft(followingText, " \t")
			expectedParen := "(" + cleanURL + ")"
			expectedParenNoSlash := "(" + strings.TrimRight(cleanURL, "/") + ")"
			if strings.HasPrefix(trimmedFollowing, expectedParen) || strings.HasPrefix(trimmedFollowing, expectedParenNoSlash) {
				alreadyFollowed = true
			}
		}

		alreadyInText := textContainsPlainURL(text, cleanURL)

		if isSameURL || alreadyFollowed || alreadyInText {
			b.WriteString(anchor)
		} else {
			// Separate any trailing whitespace or newlines from anchor to keep formatting natural.
			rightTrimmed := strings.TrimRight(anchor, " \t\r\n")
			trailingWS := anchor[len(rightTrimmed):]

			b.WriteString(rightTrimmed)
			b.WriteString(" (")
			b.WriteString(cleanURL)
			b.WriteString(")")
			b.WriteString(trailingWS)
		}

		lastPos = end
	}

	// Append any remaining text after the last link.
	if lastPos < totalLen {
		b.WriteString(string(utf16.Decode(u16[lastPos:])))
	}

	return strings.TrimLeft(b.String(), "\u200b\u200c\u200d\u2060\ufeff")
}

// FormatLinkedInPostURL builds a web URL for a published LinkedIn post URN or ID.
func FormatLinkedInPostURL(postID string) string {
	postID = strings.TrimSpace(postID)
	if postID == "" || postID == "ok" {
		return ""
	}
	if strings.HasPrefix(postID, "urn:li:") {
		return "https://www.linkedin.com/feed/update/" + strings.TrimRight(postID, "/") + "/"
	}
	return "https://www.linkedin.com/feed/update/urn:li:activity:" + strings.TrimRight(postID, "/") + "/"
}

// FormatXPostURL builds a web URL for a published tweet ID.
func FormatXPostURL(tweetID string) string {
	tweetID = strings.TrimSpace(tweetID)
	if tweetID == "" || tweetID == "ok" {
		return ""
	}
	return "https://x.com/i/status/" + tweetID
}
