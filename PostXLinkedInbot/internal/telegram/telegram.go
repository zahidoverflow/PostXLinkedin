package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Client struct {
	bot        *tgbotapi.BotAPI
	httpClient *http.Client
}

func New(bot *tgbotapi.BotAPI, httpClient *http.Client) *Client {
	return &Client{bot: bot, httpClient: httpClient}
}

func (c *Client) SendText(chatID int64, text string) (tgbotapi.Message, error) {
	return c.bot.Send(tgbotapi.NewMessage(chatID, text))
}

type DownloadedFile struct {
	Bytes    []byte
	Base64   string
	MIME     string
	Filename string
}

func BestPhoto(photos []tgbotapi.PhotoSize) tgbotapi.PhotoSize {
	// Telegram provides multiple sizes; last is usually biggest, but keep a safe max by FileSize.
	best := photos[0]
	for _, p := range photos {
		if p.FileSize > best.FileSize {
			best = p
		}
	}
	return best
}

func (c *Client) DownloadPhoto(ctx context.Context, fileID string) (DownloadedFile, error) {
	f, err := c.bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("getFile: %w", err)
	}

	url := f.Link(c.bot.Token)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return DownloadedFile{}, err
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("download: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return DownloadedFile{}, fmt.Errorf("download status: %s", res.Status)
	}

	b, err := readAllLimit(res.Body, 25<<20) // hard cap 25MB
	if err != nil {
		return DownloadedFile{}, err
	}

	// Determine MIME using headers and extension as hint.
	m := res.Header.Get("Content-Type")
	if m == "" {
		ext := path.Ext(f.FilePath)
		if ext != "" {
			m = mime.TypeByExtension(ext)
		}
	}
	if m == "" {
		m = "application/octet-stream"
	} else {
		// Strip charset, etc.
		m = strings.TrimSpace(strings.Split(m, ";")[0])
	}

	filename := path.Base(f.FilePath)
	if filename == "" || filename == "." || filename == "/" {
		filename = "photo"
	}

	return DownloadedFile{
		Bytes:    b,
		Base64:   base64.StdEncoding.EncodeToString(b),
		MIME:     m,
		Filename: filename,
	}, nil
}
