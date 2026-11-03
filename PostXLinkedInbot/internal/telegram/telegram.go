package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

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
	m := tgbotapi.NewMessage(chatID, text)
	var msg tgbotapi.Message
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		msg, err = c.bot.Send(m)
		if err == nil {
			return msg, nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return msg, err
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
	var msg tgbotapi.Message
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		msg, err = c.bot.Send(m)
		if err == nil {
			return msg, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return msg, err
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

func (c *Client) DeleteMessage(chatID int64, messageID int) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = c.bot.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		if err == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return err
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
	return c.DownloadMedia(ctx, fileID, "", "")
}

const mtprotoDownloadScript = `
import asyncio, base64, os, struct, sys
from telethon import TelegramClient
from telethon.tl.types import InputDocumentFileLocation, InputPhotoFileLocation

def rle_decode(data: bytes) -> bytes:
    out = bytearray()
    i = 0
    while i < len(data):
        if data[i] == 0:
            out.extend(b"\x00" * data[i + 1])
            i += 2
        else:
            out.append(data[i])
            i += 1
    return bytes(out)

def decode_bot_file_id(file_id: str):
    padded = file_id + "=" * (-len(file_id) % 4)
    raw = rle_decode(base64.urlsafe_b64decode(padded))
    version = raw[-1]
    body = raw[:-2] if version == 4 else raw[:-1]
    type_id, dc_id = struct.unpack("<ii", body[:8])
    has_file_ref = bool(type_id & (1 << 25))
    file_type = type_id & ~((1 << 25) | (1 << 24))
    pos = 8
    file_ref = b""
    if has_file_ref:
        ln = body[pos]
        if ln == 254:
            ln = body[pos+1] | (body[pos+2] << 8) | (body[pos+3] << 16)
            pos += 4
        else:
            pos += 1
        file_ref = body[pos:pos+ln]
        pos += ln
        rem = (pos - 8) % 4
        if rem:
            pos += 4 - rem
    media_id, access_hash = struct.unpack("<qq", body[pos:pos+16])
    return file_type, dc_id, media_id, access_hash, file_ref

async def main():
    bot_token = sys.argv[1]
    file_id = sys.argv[2]
    out_path = sys.argv[3]
    session_path = sys.argv[4]

    ft, dc, mid, ah, fref = decode_bot_file_id(file_id)
    client = TelegramClient(session_path, 2040, "b18441a1ff607e10a989891a5462e627")
    await client.start(bot_token=bot_token)
    if ft in (1, 2):
        loc = InputPhotoFileLocation(id=mid, access_hash=ah, file_reference=fref, thumb_size="y")
    else:
        loc = InputDocumentFileLocation(id=mid, access_hash=ah, file_reference=fref, thumb_size="")
    with open(out_path, "wb") as f:
        async for chunk in client.iter_download(loc, dc_id=dc, request_size=512*1024):
            f.write(chunk)
    await client.disconnect()

asyncio.run(main())
`

func (c *Client) downloadViaMTProto(ctx context.Context, fileID string) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "postxlinkedin-mtproto-*")
	if err != nil {
		return nil, fmt.Errorf("mtproto temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	sessionPath := filepath.Join(os.TempDir(), "postxlinkedinbot_mtproto_session")
	cmd := exec.CommandContext(ctx, "python3", "-c", mtprotoDownloadScript, c.bot.Token, fileID, tmpPath, sessionPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mtproto download failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	b, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("mtproto read output: %w", err)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("mtproto downloaded empty file")
	}
	return b, nil
}

func (c *Client) DownloadMedia(ctx context.Context, fileID string, preferredFilename string, preferredMIME string) (DownloadedFile, error) {
	var f tgbotapi.File
	var err error
	var b []byte
	var contentTypeHeader string

	for attempt := 0; attempt < 3; attempt++ {
		f, err = c.bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
		if err == nil {
			break
		}
		if strings.Contains(strings.ToLower(err.Error()), "file is too big") {
			break
		}
		select {
		case <-ctx.Done():
			return DownloadedFile{}, ctx.Err()
		case <-time.After(time.Duration((attempt+1)*400) * time.Millisecond):
		}
	}

	if err != nil && strings.Contains(strings.ToLower(err.Error()), "file is too big") {
		b, err = c.downloadViaMTProto(ctx, fileID)
		if err != nil {
			return DownloadedFile{}, err
		}
	} else if err != nil {
		return DownloadedFile{}, fmt.Errorf("getFile: %w", err)
	} else {
		url := f.Link(c.bot.Token)
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return DownloadedFile{}, ctx.Err()
				case <-time.After(time.Duration(attempt*500) * time.Millisecond):
				}
			}
			req, reqErr := http.NewRequestWithContext(ctx, "GET", url, nil)
			if reqErr != nil {
				return DownloadedFile{}, reqErr
			}
			res, doErr := c.httpClient.Do(req)
			if doErr != nil {
				err = fmt.Errorf("download: %w", doErr)
				continue
			}
			if res.StatusCode < 200 || res.StatusCode >= 300 {
				err = fmt.Errorf("download status: %s", res.Status)
				res.Body.Close()
				continue
			}
			contentTypeHeader = res.Header.Get("Content-Type")
			b, err = readAllLimit(res.Body, 100<<20) // hard cap 100MB (LinkedIn document limit)
			res.Body.Close()
			if err == nil {
				break
			}
		}
		if err != nil {
			return DownloadedFile{}, err
		}
	}

	// Determine MIME: prefer preferredMIME if valid, then Content-Type header, then extension, then byte sniffing.
	m := strings.TrimSpace(strings.Split(preferredMIME, ";")[0])
	if m == "" || m == "application/octet-stream" {
		m = strings.TrimSpace(strings.Split(contentTypeHeader, ";")[0])
	}
	if m == "" || m == "application/octet-stream" {
		targetPath := f.FilePath
		if preferredFilename != "" {
			targetPath = preferredFilename
		}
		if ext := path.Ext(targetPath); ext != "" {
			if byExt := mime.TypeByExtension(ext); byExt != "" {
				m = byExt
			}
		}
	}
	if m == "" || m == "application/octet-stream" {
		// Sniff actual bytes — reliably detects JPEG, PNG, GIF, WebP, PDF, etc.
		m = http.DetectContentType(b)
		m = strings.TrimSpace(strings.Split(m, ";")[0])
	}

	filename := preferredFilename
	if filename == "" {
		filename = path.Base(f.FilePath)
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "media"
	}

	b64 := ""
	if len(b) <= 16<<20 {
		b64 = base64.StdEncoding.EncodeToString(b)
	}

	return DownloadedFile{
		Bytes:    b,
		Base64:   b64,
		MIME:     m,
		Filename: filename,
	}, nil
}
