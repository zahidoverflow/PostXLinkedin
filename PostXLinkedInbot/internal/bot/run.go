package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/linkedin"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/n8n"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/telegram"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/x"
)

func Run(ctx context.Context, logger *log.Logger, pollTimeout time.Duration) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	rt := Runtime{PollTimeout: pollTimeout}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return fmt.Errorf("telegram init: %w", err)
	}
	bot.Debug = cfg.Debug

	httpClient := &http.Client{Timeout: 60 * time.Second}
	tg := telegram.New(bot, httpClient)

	var n8 *n8n.Client
	if cfg.N8NWebhookURL != "" {
		n8 = n8n.NewClient(httpClient, cfg.N8NWebhookURL, cfg.N8NSharedSecret)
	}

	logger.Printf("telegram bot started as @%s", bot.Self.UserName)
	if cfg.AllowedChatID != 0 {
		logger.Printf("restricted to ALLOWED_CHAT_ID=%d", cfg.AllowedChatID)
	}

	updateCfg := tgbotapi.NewUpdate(0)
	updateCfg.Timeout = int(rt.PollTimeout.Seconds())
	updates := bot.GetUpdatesChan(updateCfg)

	for {
		select {
		case <-ctx.Done():
			logger.Printf("shutdown requested")
			return nil
		case upd := <-updates:
			if upd.Message == nil {
				continue
			}
			if cfg.AllowedChatID != 0 && upd.Message.Chat != nil && upd.Message.Chat.ID != cfg.AllowedChatID {
				// Silent ignore to avoid leaking.
				continue
			}
			if err := handleMessage(ctx, logger, cfg, tg, n8, upd.Message); err != nil {
				logger.Printf("handleMessage error: %v", err)
			}
		}
	}
}

func handleMessage(ctx context.Context, logger *log.Logger, cfg Config, tg *telegram.Client, n8 *n8n.Client, msg *tgbotapi.Message) error {
	chatID := msg.Chat.ID

	if msg.IsCommand() {
		switch msg.Command() {
		case "start", "help":
			help := "Send a photo with a caption.\n\nThe bot will forward it to n8n, which posts to X and LinkedIn."
			_, _ = tg.SendText(chatID, help)
			return nil
		case "ping":
			_, _ = tg.SendText(chatID, "pong")
			return nil
		default:
			_, _ = tg.SendText(chatID, "Unknown command. Use /help.")
			return nil
		}
	}

	if len(msg.Photo) == 0 {
		_, _ = tg.SendText(chatID, "Send a photo with a caption (text).")
		return nil
	}
	if msg.Caption == "" {
		_, _ = tg.SendText(chatID, "Missing caption. Add a caption to your photo (this becomes the post text).")
		return nil
	}

	photo := telegram.BestPhoto(msg.Photo)

	if cfg.Debug {
		logger.Printf("photo received: chat=%d file_id=%s size=%dx%d file_size=%d caption_len=%d",
			chatID, photo.FileID, photo.Width, photo.Height, photo.FileSize, len(msg.Caption))
	}

	// Tell user quickly we started.
	_, _ = tg.SendText(chatID, "Posting...")

	dl, err := tg.DownloadPhoto(ctx, photo.FileID)
	if err != nil {
		_, _ = tg.SendText(chatID, "Failed to download photo from Telegram.")
		return err
	}
	if int64(len(dl.Bytes)) > cfg.MaxImageBytes {
		_, _ = tg.SendText(chatID, fmt.Sprintf("Image too large (%d bytes). Max allowed is %d bytes.", len(dl.Bytes), cfg.MaxImageBytes))
		return errors.New("image too large")
	}

	// Mode 1: n8n webhook.
	if n8 != nil {
		req := n8n.PostRequest{
			Caption:       msg.Caption,
			ImageBase64:   dl.Base64,
			ImageMIME:     dl.MIME,
			ImageFilename: dl.Filename,
			Telegram: n8n.TelegramMeta{
				ChatID:    chatID,
				MessageID: msg.MessageID,
				From: n8n.TelegramFrom{
					ID:        msg.From.ID,
					UserName:  msg.From.UserName,
					FirstName: msg.From.FirstName,
					LastName:  msg.From.LastName,
				},
			},
		}

		resp, err := n8.Post(ctx, req)
		if err != nil {
			_, _ = tg.SendText(chatID, "Posting failed (n8n error). Check server logs.")
			return err
		}
		if resp.OK {
			_, _ = tg.SendText(chatID, "Posted successfully.")
			return nil
		}
		_, _ = tg.SendText(chatID, fmt.Sprintf("Posting failed: %s", resp.Error))
		return fmt.Errorf("n8n failed: %s", resp.Error)
	}

	// Mode 2: direct API calls to X + LinkedIn.
	// Recreate clients here (cheap) to keep handleMessage signature small.
	httpClient := &http.Client{Timeout: 60 * time.Second}
	var errs []string

	if cfg.EnableX {
		xClient := x.New(httpClient, cfg.XAPIBaseURL, cfg.XUserBearerToken)
		mediaID, err := xClient.UploadMedia(ctx, dl.Base64, dl.MIME)
		if err != nil {
			errs = append(errs, "X upload: "+err.Error())
		} else {
			if _, err := xClient.CreatePost(ctx, msg.Caption, []string{mediaID}); err != nil {
				errs = append(errs, "X post: "+err.Error())
			}
		}
	}

	if cfg.EnableLinkedIn {
		liClient := linkedin.New(httpClient, cfg.LinkedInAccessToken, cfg.LinkedInVersion)
		uploadURL, imageURN, err := liClient.InitializeImageUpload(ctx, cfg.LinkedInAuthorURN)
		if err != nil {
			errs = append(errs, "LinkedIn init: "+err.Error())
		} else if err := liClient.UploadImageBytes(ctx, uploadURL, dl.MIME, dl.Bytes); err != nil {
			errs = append(errs, "LinkedIn upload: "+err.Error())
		} else if _, err := liClient.CreateImagePost(ctx, cfg.LinkedInAuthorURN, msg.Caption, imageURN, dl.Filename); err != nil {
			errs = append(errs, "LinkedIn post: "+err.Error())
		}
	}

	if len(errs) == 0 {
		_, _ = tg.SendText(chatID, "Posted successfully.")
		return nil
	}
	_, _ = tg.SendText(chatID, "Posting failed:\n- "+strings.Join(errs, "\n- "))
	return fmt.Errorf("posting failed: %s", strings.Join(errs, "; "))
}
