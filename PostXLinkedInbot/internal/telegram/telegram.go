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

func (c *Client) SendTextWithKeyboard(chatID int64, text string, keyboard tgbotapi.ReplyKeyboardMarkup) (tgbotapi.Message, error) {
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyMarkup = keyboard
	return c.bot.Send(m)
}

func (c *Client) SendTextRemoveKeyboard(chatID int64, text string) (tgbotapi.Message, error) {
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	return c.bot.Send(m)
}

func (c *Client) SendHTML(chatID int64, html string) (tgbotapi.Message, error) {
	m := tgbotapi.NewMessage(chatID, html)
	m.ParseMode = tgbotapi.ModeHTML
	m.DisableWebPagePreview = true
	return c.bot.Send(m)
}

func (c *Client) SendHTMLWithKeyboard(chatID int64, html string, keyboard tgbotapi.ReplyKeyboardMarkup) (tgbotapi.Message, error) {
	m := tgbotapi.NewMessage(chatID, html)
	m.ParseMode = tgbotapi.ModeHTML
	m.DisableWebPagePreview = true
	m.ReplyMarkup = keyboard
	return c.bot.Send(m)
}

func (c *Client) SendHTMLRemoveKeyboard(chatID int64, html string) (tgbotapi.Message, error) {
	m := tgbotapi.NewMessage(chatID, html)
	m.ParseMode = tgbotapi.ModeHTML
	m.DisableWebPagePreview = true
	m.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	return c.bot.Send(m)
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

	// Determine MIME: prefer Content-Type header, then extension, then byte sniffing.
	m := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	if m == "" || m == "application/octet-stream" {
		if ext := path.Ext(f.FilePath); ext != "" {
			if byExt := mime.TypeByExtension(ext); byExt != "" {
				m = byExt
			}
		}
	}
	if m == "" || m == "application/octet-stream" {
		// Sniff actual bytes — reliably detects JPEG, PNG, GIF, WebP, etc.
		m = http.DetectContentType(b)
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
