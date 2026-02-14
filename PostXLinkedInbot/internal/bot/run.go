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
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/agent"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/linkedin"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/n8n"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/setup"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/store"
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

	// Load persisted setup (if any) and merge it over env config for posting-related fields.
	stored, ok, err := store.Load(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("load CONFIG_PATH: %w", err)
	}
	if ok {
		cfg = mergeStored(cfg, stored)
	}

	logger.Printf("telegram bot started as @%s", bot.Self.UserName)
	if cfg.AllowedChatID != 0 {
		logger.Printf("restricted to ALLOWED_CHAT_ID=%d", cfg.AllowedChatID)
	}
	logger.Printf("config path: %s", cfg.ConfigPath)

	var n8 *n8n.Client
	if cfg.N8NWebhookURL != "" {
		n8 = n8n.NewClient(httpClient, cfg.N8NWebhookURL, cfg.N8NSharedSecret)
	}

	// Setup wizard sessions (per-chat).
	sessions := map[int64]*setup.Wizard{}

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

			chatID := int64(0)
			if upd.Message.Chat != nil {
				chatID = upd.Message.Chat.ID
			}

			if cfg.AllowedChatID != 0 && chatID != 0 && chatID != cfg.AllowedChatID {
				// Silent ignore to avoid leaking.
				continue
			}

			if err := handleMessage(ctx, logger, &cfg, tg, n8, sessions, upd.Message); err != nil {
				logger.Printf("handleMessage error: %v", err)
			}
		}
	}
}

func handleMessage(ctx context.Context, logger *log.Logger, cfg *Config, tg *telegram.Client, n8 *n8n.Client, sessions map[int64]*setup.Wizard, msg *tgbotapi.Message) error {
	chatID := msg.Chat.ID

	// Setup wizard active: consume plain text replies.
	if w := sessions[chatID]; w != nil && msg.Text != "" && !msg.IsCommand() {
		xClient := x.New(&http.Client{Timeout: 15 * time.Second}, cfg.XAPIBaseURL, w.Draft.XUserBearerToken)
		liClient := linkedin.New(&http.Client{Timeout: 15 * time.Second}, w.Draft.LinkedInAccessToken, w.Draft.LinkedInVersion)

		done, newCfg, werr := w.HandleText(ctx, tg, xClient, liClient, msg.Text)
		if done {
			delete(sessions, chatID)
			if werr != nil {
				return nil
			}
			if err := store.Save(cfg.ConfigPath, newCfg); err != nil {
				_, _ = tg.SendText(chatID, "Failed to save config on disk. Check file permissions.")
				return err
			}
			// Merge and activate new config immediately.
			*cfg = mergeStored(*cfg, newCfg)
			// Rebuild n8n client on next post (or now).
			return nil
		}
		return nil
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "start", "help":
			if !isConfigured(*cfg) {
				// Start setup wizard.
				w := setup.New(chatID)
				sessions[chatID] = w
				_, _ = tg.SendText(chatID, "This bot needs a one-time setup.\n\nSecurity note: tokens you paste here can post to your accounts. Use private chat and lock the bot to this chat.\n\nYou can /cancel anytime.")
				w.Start(tg)
				return nil
			}
			help := "Send a photo with a caption.\n\nCommands:\n/setup - reconfigure\n/status - show status\n/cancel - cancel setup (if running)"
			_, _ = tg.SendText(chatID, help)
			return nil
		case "setup":
			w := setup.New(chatID)
			sessions[chatID] = w
			_, _ = tg.SendText(chatID, "Starting setup wizard. You can /cancel anytime.")
			w.Start(tg)
			return nil
		case "status":
			_, _ = tg.SendText(chatID, statusText(*cfg))
			return nil
		case "cancel":
			if sessions[chatID] != nil {
				delete(sessions, chatID)
				_, _ = tg.SendTextRemoveKeyboard(chatID, "Setup cancelled. Use /setup to start again.")
				return nil
			}
			_, _ = tg.SendText(chatID, "No setup is running.")
			return nil
		case "ping":
			_, _ = tg.SendText(chatID, "pong")
			return nil
		default:
			_, _ = tg.SendText(chatID, "Unknown command. Use /help.")
			return nil
		}
	}

	if !isConfigured(*cfg) {
		_, _ = tg.SendText(chatID, "Not configured yet. Use /start or /setup.")
		return nil
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

	caption := msg.Caption
	// Optional agent step: rewrite caption before posting.
	if cfg.AgentWebhookURL != "" {
		targets := []string{}
		if cfg.EnableX {
			targets = append(targets, "x")
		}
		if cfg.EnableLinkedIn {
			targets = append(targets, "linkedin")
		}
		a := agent.New(&http.Client{Timeout: 20 * time.Second}, cfg.AgentWebhookURL, cfg.AgentSharedSecret)
		if newCaption, err := a.ProcessCaption(ctx, caption, targets); err != nil {
			_, _ = tg.SendText(chatID, "Agent webhook failed; posting original caption.")
			logger.Printf("agent error: %v", err)
		} else {
			caption = newCaption
		}
	}

	if cfg.EnableX && cfg.XUserBearerToken != "" {
		xClient := x.New(httpClient, cfg.XAPIBaseURL, cfg.XUserBearerToken)
		mediaID, err := xClient.UploadMedia(ctx, dl.Base64, dl.MIME)
		if err != nil {
			errs = append(errs, "X upload: "+err.Error())
		} else {
			xText := caption
			if len([]rune(xText)) > 280 {
				xText = truncateRunes(xText, 277) + "..."
			}
			if _, err := xClient.CreatePost(ctx, xText, []string{mediaID}); err != nil {
				errs = append(errs, "X post: "+err.Error())
			}
		}
	}

	if cfg.EnableLinkedIn && cfg.LinkedInAccessToken != "" && cfg.LinkedInAuthorURN != "" {
		liClient := linkedin.New(httpClient, cfg.LinkedInAccessToken, cfg.LinkedInVersion)
		uploadURL, imageURN, err := liClient.InitializeImageUpload(ctx, cfg.LinkedInAuthorURN)
		if err != nil {
			errs = append(errs, "LinkedIn init: "+err.Error())
		} else if err := liClient.UploadImageBytes(ctx, uploadURL, dl.MIME, dl.Bytes); err != nil {
			errs = append(errs, "LinkedIn upload: "+err.Error())
		} else if _, err := liClient.CreateImagePost(ctx, cfg.LinkedInAuthorURN, caption, imageURN, dl.Filename); err != nil {
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

func mergeStored(cfg Config, stored store.Config) Config {
	// Allow stored AllowedChatID to override env.
	if stored.AllowedChatID != 0 {
		cfg.AllowedChatID = stored.AllowedChatID
	}
	if stored.MaxImageBytes > 0 {
		cfg.MaxImageBytes = stored.MaxImageBytes
	}

	if stored.Mode == store.ModeN8N {
		cfg.N8NWebhookURL = stored.N8NWebhookURL
		cfg.N8NSharedSecret = stored.N8NSharedSecret
		cfg.AgentWebhookURL = stored.AgentWebhookURL
		cfg.AgentSharedSecret = stored.AgentSharedSecret
		// In n8n mode we leave direct creds as-is (unused).
		return cfg
	}

	// Direct mode.
	cfg.N8NWebhookURL = ""
	cfg.N8NSharedSecret = ""

	cfg.EnableX = stored.EnableX
	cfg.EnableLinkedIn = stored.EnableLinkedIn

	cfg.AgentWebhookURL = stored.AgentWebhookURL
	cfg.AgentSharedSecret = stored.AgentSharedSecret

	if stored.XAPIBaseURL != "" {
		cfg.XAPIBaseURL = stored.XAPIBaseURL
	}
	if stored.XUserBearerToken != "" {
		cfg.XUserBearerToken = stored.XUserBearerToken
	}
	if stored.LinkedInVersion != "" {
		cfg.LinkedInVersion = stored.LinkedInVersion
	}
	if stored.LinkedInAccessToken != "" {
		cfg.LinkedInAccessToken = stored.LinkedInAccessToken
	}
	if stored.LinkedInAuthorURN != "" {
		cfg.LinkedInAuthorURN = stored.LinkedInAuthorURN
	}
	return cfg
}

func isConfigured(cfg Config) bool {
	if cfg.N8NWebhookURL != "" {
		return true
	}
	if cfg.EnableX && cfg.XUserBearerToken != "" {
		return true
	}
	if cfg.EnableLinkedIn && cfg.LinkedInAccessToken != "" && cfg.LinkedInAuthorURN != "" {
		return true
	}
	return false
}

func statusText(cfg Config) string {
	mode := "direct"
	if cfg.N8NWebhookURL != "" {
		mode = "n8n"
	}
	var b strings.Builder
	b.WriteString("Status:\n")
	b.WriteString("- mode: " + mode + "\n")
	if cfg.AllowedChatID != 0 {
		b.WriteString(fmt.Sprintf("- locked chat: %d\n", cfg.AllowedChatID))
	} else {
		b.WriteString("- locked chat: no\n")
	}
	if mode == "n8n" {
		b.WriteString("- n8n webhook: set\n")
		if cfg.N8NSharedSecret != "" {
			b.WriteString("- n8n secret: set\n")
		} else {
			b.WriteString("- n8n secret: not set\n")
		}
		if cfg.AgentWebhookURL != "" {
			b.WriteString("- agent: enabled\n")
		} else {
			b.WriteString("- agent: disabled\n")
		}
		return b.String()
	}
	b.WriteString(fmt.Sprintf("- X enabled: %t\n", cfg.EnableX))
	b.WriteString(fmt.Sprintf("- LinkedIn enabled: %t\n", cfg.EnableLinkedIn))
	if cfg.AgentWebhookURL != "" {
		b.WriteString("- agent: enabled\n")
	} else {
		b.WriteString("- agent: disabled\n")
	}
	b.WriteString(fmt.Sprintf("- X token: %s\n", redact(cfg.XUserBearerToken)))
	b.WriteString(fmt.Sprintf("- LinkedIn token: %s\n", redact(cfg.LinkedInAccessToken)))
	if cfg.LinkedInAuthorURN != "" {
		b.WriteString("- LinkedIn author URN: set\n")
	} else {
		b.WriteString("- LinkedIn author URN: not set\n")
	}
	return b.String()
}

func redact(s string) string {
	if s == "" {
		return "not set"
	}
	if len(s) <= 8 {
		return "set"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
