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
			if upd.CallbackQuery != nil {
				// Acknowledge callback queries (future inline-keyboard support).
				cb := tgbotapi.NewCallback(upd.CallbackQuery.ID, "")
				_, _ = bot.Request(cb)
				continue
			}

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

	// Setup wizard active: consume plain text replies but reject photos/media during setup.
	if w := sessions[chatID]; w != nil {
		// If user sends a photo/sticker/document during setup, tell them to finish first.
		if len(msg.Photo) > 0 || msg.Document != nil || msg.Sticker != nil || msg.Video != nil {
			_, _ = tg.SendText(chatID, "Please finish setup first, or /cancel to exit setup.")
			return nil
		}
		if msg.Text != "" && !msg.IsCommand() {
			done, newCfg, werr := w.HandleText(ctx, tg, msg.Text)
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
				return nil
			}
			return nil
		}
	}

	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			if !isConfigured(*cfg) {
				// Start setup wizard for unconfigured bot.
				w := setup.New(chatID)
				sessions[chatID] = w
				_, _ = tg.SendHTML(chatID, "\U0001f44b <b>Welcome to PostXLinkedInBot!</b>\n\nThis bot posts your content to <b>X (Twitter)</b> and <b>LinkedIn</b> simultaneously.\n\nLet's set it up \u2014 I'll guide you step-by-step.\n\n\u26a0\ufe0f <b>Security:</b> tokens you paste can post to your accounts.\nUse a <b>private chat</b> and delete token messages after setup.\n\nYou can /cancel anytime.")
				w.Start(tg)
				return nil
			}
			_, _ = tg.SendHTML(chatID, "\U0001f44b <b>PostXLinkedInBot is ready!</b>\n\n\U0001f4f8 Send a <b>photo with a caption</b> to post to your connected platforms.\n\U0001f4dd Or send a <b>text message</b> to post text-only.\n\nUse /help to see all commands.")
			return nil
		case "help":
			help := "\U0001f4d6 <b>PostXLinkedInBot \u2014 Commands</b>\n\n" +
				"\U0001f4f8 <b>Post content:</b>\n" +
				"\u2022 Send a <b>photo with caption</b> \u2192 image post\n" +
				"\u2022 Send a <b>text message</b> \u2192 text-only post\n\n" +
				"\u2699\ufe0f <b>Configuration:</b>\n" +
				"/setup \u2014 full setup wizard\n" +
				"/x \u2014 setup X (Twitter) only\n" +
				"/linkedin \u2014 setup LinkedIn only\n" +
				"/stop_x \u2014 stop posting to X\n" +
				"/stop_linkedin \u2014 stop posting to LinkedIn\n" +
				"/status \u2014 show current config summary\n" +
				"/guide \u2014 step-by-step setup instructions\n\n" +
				"\U0001f6e0\ufe0f <b>Utility:</b>\n" +
				"/cancel \u2014 cancel setup (if running)\n" +
				"/ping \u2014 check bot is alive\n" +
				"/version \u2014 show bot version"
			_, _ = tg.SendHTML(chatID, help)
			return nil
		case "setup":
			w := setup.New(chatID)
			sessions[chatID] = w
			_, _ = tg.SendHTML(chatID, "\U0001f527 <b>Starting setup wizard.</b>\n\nNeed help getting tokens? Use /guide first.\nYou can /cancel anytime.")
			w.Start(tg)
			return nil
		case "x":
			w := setup.NewForX(chatID)
			// Pre-load existing config so we don't wipe the other platform's settings.
			if stored, ok, _ := store.Load(cfg.ConfigPath); ok {
				w.PreloadDraft(stored)
			}
			sessions[chatID] = w
			_, _ = tg.SendHTML(chatID, "\U0001f426 <b>X (Twitter) setup</b>\n\nLet's connect your X account. You can /cancel anytime.")
			w.StartAtCurrentStep(tg)
			return nil
		case "linkedin":
			w := setup.NewForLinkedIn(chatID)
			// Pre-load existing config so we don't wipe the other platform's settings.
			if stored, ok, _ := store.Load(cfg.ConfigPath); ok {
				w.PreloadDraft(stored)
			}
			sessions[chatID] = w
			_, _ = tg.SendHTML(chatID, "\U0001f4bc <b>LinkedIn setup</b>\n\nLet's connect your LinkedIn account. You can /cancel anytime.")
			w.StartAtCurrentStep(tg)
			return nil
		case "stop_x":
			if !cfg.EnableX {
				_, _ = tg.SendText(chatID, "X posting is already disabled.")
				return nil
			}
			cfg.EnableX = false
			if stored, ok, _ := store.Load(cfg.ConfigPath); ok {
				stored.EnableX = false
				_ = store.Save(cfg.ConfigPath, stored)
			}
			_, _ = tg.SendHTML(chatID, "\u2705 <b>X posting disabled.</b>\n\nYour tokens are kept. Use /x to re-enable anytime.")
			return nil
		case "stop_linkedin":
			if !cfg.EnableLinkedIn {
				_, _ = tg.SendText(chatID, "LinkedIn posting is already disabled.")
				return nil
			}
			cfg.EnableLinkedIn = false
			if stored, ok, _ := store.Load(cfg.ConfigPath); ok {
				stored.EnableLinkedIn = false
				_ = store.Save(cfg.ConfigPath, stored)
			}
			_, _ = tg.SendHTML(chatID, "\u2705 <b>LinkedIn posting disabled.</b>\n\nYour tokens are kept. Use /linkedin to re-enable anytime.")
			return nil
		case "status":
			_, _ = tg.SendHTML(chatID, statusText(*cfg))
			return nil
		case "guide":
			_, _ = tg.SendHTML(chatID, guideText())
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
			_, _ = tg.SendText(chatID, "pong \U0001f3d3")
			return nil
		case "version":
			_, _ = tg.SendHTML(chatID, "\U0001f916 <b>PostXLinkedInBot</b> v1.1.0\nLinkedIn API: <code>"+cfg.LinkedInVersion+"</code>\nX API: v2")
			return nil
		default:
			_, _ = tg.SendText(chatID, "Unknown command. Use /help to see available commands.")
			return nil
		}
	}

	if !isConfigured(*cfg) {
		_, _ = tg.SendHTML(chatID, "Not configured yet. Use /start or /setup to get started.")
		return nil
	}

	// Catch stale keyboard buttons (e.g. "⬅️ Back") from a finished/cancelled setup wizard.
	if isStaleKeyboardButton(msg.Text) {
		_, _ = tg.SendTextRemoveKeyboard(chatID, "No setup is running. Use /help to see commands.")
		return nil
	}

	// Determine post type: photo+caption, caption-only photo, or text-only.
	hasPhoto := len(msg.Photo) > 0
	postText := msg.Text // text-only message
	if hasPhoto {
		postText = msg.Caption
	}

	// Reject unsupported media types.
	if msg.Document != nil || msg.Video != nil || msg.Sticker != nil || msg.Voice != nil || msg.Audio != nil {
		_, _ = tg.SendHTML(chatID, "\u274c Unsupported media type.\n\n\U0001f4f8 Send a <b>photo with caption</b> for an image post.\n\U0001f4dd Or send a <b>text message</b> for a text-only post.")
		return nil
	}

	if hasPhoto && postText == "" {
		_, _ = tg.SendText(chatID, "Missing caption. Add a caption to your photo (this becomes the post text).")
		return nil
	}
	if !hasPhoto && postText == "" {
		_, _ = tg.SendHTML(chatID, "\U0001f4f8 Send a <b>photo with caption</b> for an image post.\n\U0001f4dd Or send a <b>text message</b> for a text-only post.")
		return nil
	}

	var dl *telegram.DownloadedFile
	if hasPhoto {
		photo := telegram.BestPhoto(msg.Photo)
		if cfg.Debug {
			logger.Printf("photo received: chat=%d file_id=%s size=%dx%d file_size=%d caption_len=%d",
				chatID, photo.FileID, photo.Width, photo.Height, photo.FileSize, len(postText))
		}

		_, _ = tg.SendText(chatID, "\u23f3 Downloading image...")

		d, err := tg.DownloadPhoto(ctx, photo.FileID)
		if err != nil {
			_, _ = tg.SendHTML(chatID, "\u274c Failed to download photo from Telegram. Try sending it again.")
			return err
		}
		if int64(len(d.Bytes)) > cfg.MaxImageBytes {
			_, _ = tg.SendHTML(chatID, fmt.Sprintf("\u274c Image too large (%d bytes). Max allowed is %d bytes.\n\nTry compressing or resizing the image.", len(d.Bytes), cfg.MaxImageBytes))
			return errors.New("image too large")
		}
		dl = &d
	} else {
		if cfg.Debug {
			logger.Printf("text post received: chat=%d text_len=%d", chatID, len(postText))
		}
	}

	caption := postText
	// Sanitize newlines: remove carriage returns to avoid truncation issues on some platforms.
	caption = strings.ReplaceAll(caption, "\r", "")

	// Mode 1: n8n webhook (only supports image posts).
	if n8 != nil {
		if dl == nil {
			_, _ = tg.SendText(chatID, "n8n mode requires a photo with caption. Text-only posts are not supported in n8n mode.")
			return nil
		}
		_, _ = tg.SendText(chatID, "\u23f3 Posting via n8n...")

		req := n8n.PostRequest{
			Caption:       caption,
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
			_, _ = tg.SendHTML(chatID, "\u274c <b>Posting failed</b> (n8n error).\n\nCheck server logs or verify your n8n webhook is running.")
			return err
		}
		if resp.OK {
			_, _ = tg.SendHTML(chatID, "\u2705 <b>Posted successfully</b> via n8n!")
			return nil
		}
		_, _ = tg.SendHTML(chatID, fmt.Sprintf("\u274c <b>Posting failed:</b> %s", escapeHTML(resp.Error)))
		return fmt.Errorf("n8n failed: %s", resp.Error)
	}

	// Mode 2: direct API calls to X + LinkedIn.
	httpClient := &http.Client{Timeout: 60 * time.Second}
	var results []string
	var errs []string

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
			_, _ = tg.SendText(chatID, "\u26a0\ufe0f Agent webhook failed; posting original caption.")
			logger.Printf("agent error: %v", err)
		} else {
			if newCaption != caption {
				_, _ = tg.SendText(chatID, "\U0001f916 Caption rewritten by agent.")
			}
			caption = strings.ReplaceAll(newCaption, "\r", "")
		}
	}

	if cfg.EnableX && cfg.XUserBearerToken != "" {
		_, _ = tg.SendText(chatID, "\u23f3 Posting to X...")

		// Auto-refresh X token if we have credentials.
		if cfg.XRefreshToken != "" && cfg.XClientID != "" && cfg.XClientSecret != "" {
			if refreshed, err := tryRefreshXToken(ctx, logger, cfg); err == nil {
				*cfg = refreshed
			}
		} else {
			logger.Printf("X token refresh skipped: missing refresh_token or client credentials")
		}
		xClient := x.New(httpClient, cfg.XAPIBaseURL, cfg.XUserBearerToken)

		xText := caption
		if len([]rune(xText)) > 280 {
			xText = truncateRunes(xText, 277) + "..."
		}

		if dl != nil {
			// Image post.
			mediaID, err := xClient.UploadMedia(ctx, dl.Base64, dl.MIME)
			if err != nil {
				errs = append(errs, "X upload: "+err.Error())
				if isXAuthError(err) {
					errs = append(errs, xAuthHint)
				}
			} else {
				if tweetID, err := xClient.CreatePost(ctx, xText, []string{mediaID}); err != nil {
					errs = append(errs, "X post: "+err.Error())
					if isXAuthError(err) {
						errs = append(errs, xAuthHint)
					}
				} else {
					results = append(results, "\u2705 X: posted (ID: "+tweetID+")")
				}
			}
		} else {
			// Text-only post.
			if tweetID, err := xClient.CreatePost(ctx, xText, nil); err != nil {
				errs = append(errs, "X post: "+err.Error())
				if isXAuthError(err) {
					errs = append(errs, xAuthHint)
				}
			} else {
				results = append(results, "\u2705 X: posted (ID: "+tweetID+")")
			}
		}
	}

	if cfg.EnableLinkedIn && cfg.LinkedInAccessToken != "" && cfg.LinkedInAuthorURN != "" {
		_, _ = tg.SendText(chatID, "\u23f3 Posting to LinkedIn...")

		liClient := linkedin.New(httpClient, cfg.LinkedInAccessToken, cfg.LinkedInVersion)

		if dl != nil {
			// Image post.
			uploadURL, imageURN, err := liClient.InitializeImageUpload(ctx, cfg.LinkedInAuthorURN)
			if err != nil {
				errs = append(errs, "LinkedIn init: "+err.Error())
			} else if err := liClient.UploadImageBytes(ctx, uploadURL, dl.MIME, dl.Bytes); err != nil {
				errs = append(errs, "LinkedIn upload: "+err.Error())
			} else if postID, err := liClient.CreateImagePost(ctx, cfg.LinkedInAuthorURN, caption, imageURN, dl.Filename); err != nil {
				errs = append(errs, "LinkedIn post: "+err.Error())
			} else {
				results = append(results, "\u2705 LinkedIn: posted (ID: "+postID+")")
			}
		} else {
			// Text-only post.
			if postID, err := liClient.CreateTextPost(ctx, cfg.LinkedInAuthorURN, caption); err != nil {
				errs = append(errs, "LinkedIn post: "+err.Error())
			} else {
				results = append(results, "\u2705 LinkedIn: posted (ID: "+postID+")")
			}
		}
	}

	// Build summary message.
	var summary strings.Builder
	if len(results) > 0 && len(errs) == 0 {
		summary.WriteString("\u2705 <b>Posted successfully!</b>\n\n")
		for _, r := range results {
			summary.WriteString(r + "\n")
		}
	} else if len(results) > 0 && len(errs) > 0 {
		summary.WriteString("\u26a0\ufe0f <b>Partially posted:</b>\n\n")
		for _, r := range results {
			summary.WriteString(r + "\n")
		}
		summary.WriteString("\n<b>Errors:</b>\n")
		for _, e := range errs {
			summary.WriteString("\u274c " + escapeHTML(e) + "\n")
		}
	} else {
		summary.WriteString("\u274c <b>Posting failed:</b>\n\n")
		for _, e := range errs {
			summary.WriteString("\u274c " + escapeHTML(e) + "\n")
		}
		summary.WriteString("\n\U0001f4a1 <b>Tip:</b> Run /status to check your config, or /setup to reconfigure.")
	}
	_, _ = tg.SendHTML(chatID, summary.String())

	if len(errs) > 0 {
		return fmt.Errorf("posting failed: %s", strings.Join(errs, "; "))
	}
	return nil
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
	if stored.XRefreshToken != "" {
		cfg.XRefreshToken = stored.XRefreshToken
	}
	if stored.XClientID != "" {
		cfg.XClientID = stored.XClientID
	}
	if stored.XClientSecret != "" {
		cfg.XClientSecret = stored.XClientSecret
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

func guideText() string {
	return "\U0001f4d6 <b>Setup Guide \u2014 PostXLinkedInBot</b>\n" +
		"\n" +
		"<b>\U0001f426 X (Twitter):</b>\n" +
		"1. <a href=\"https://developer.x.com/en/portal/petition/essential/basic-info\">Sign up</a> for X Developer (Free tier)\n" +
		"2. <a href=\"https://developer.x.com/en/portal/dashboard\">Create a Project + App</a>\n" +
		"3. App Settings \u2192 User Auth \u2192 OAuth 2.0:\n" +
		"   Permissions: Read+Write, Type: Web App\n" +
		"   Callback: <code>https://127.0.0.1/callback</code>\n" +
		"4. During /setup, choose \"OAuth flow\" \u2014 paste your Client ID + Secret\n" +
		"   The bot handles the rest (PKCE S256 + token auto-refresh)!\n" +
		"\U0001f4d6 <a href=\"https://docs.x.com/resources/fundamentals/authentication/oauth-2-0/authorization-code\">X OAuth 2.0 Docs</a>\n" +
		"\n" +
		"<b>\U0001f4bc LinkedIn:</b>\n" +
		"1. <a href=\"https://www.linkedin.com/developers/apps/new\">Create a LinkedIn App</a>\n" +
		"2. Products tab \u2192 Request \"Share on LinkedIn\" + \"Sign In with LinkedIn using OpenID Connect\"\n" +
		"3. <a href=\"https://www.linkedin.com/developers/tools/oauth/token-generator\">Generate token</a> with scopes: <code>openid</code>, <code>profile</code>, <code>w_member_social</code>\n" +
		"\u26a0\ufe0f Tokens expire in ~60 days. Run /setup again to refresh.\n" +
		"\U0001f4d6 <a href=\"https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api\">LinkedIn Posts API Docs</a>\n" +
		"\U0001f4d6 <a href=\"https://learn.microsoft.com/en-us/linkedin/marketing/versioning\">LinkedIn API Versioning</a>\n" +
		"\n" +
		"<b>\U0001f504 To start setup:</b> /setup"
}

func statusText(cfg Config) string {
	mode := "direct"
	if cfg.N8NWebhookURL != "" {
		mode = "n8n"
	}
	var b strings.Builder
	b.WriteString("\U0001f4ca <b>Bot Status</b>\n\n")
	b.WriteString("\u2022 <b>Mode:</b> " + mode + "\n")
	if cfg.AllowedChatID != 0 {
		b.WriteString(fmt.Sprintf("\u2022 <b>Locked chat:</b> %d\n", cfg.AllowedChatID))
	} else {
		b.WriteString("\u2022 <b>Locked chat:</b> no\n")
	}
	if mode == "n8n" {
		b.WriteString("\u2022 <b>n8n webhook:</b> set\n")
		if cfg.N8NSharedSecret != "" {
			b.WriteString("\u2022 <b>n8n secret:</b> set\n")
		} else {
			b.WriteString("\u2022 <b>n8n secret:</b> \u274c not set\n")
		}
		if cfg.AgentWebhookURL != "" {
			b.WriteString("\u2022 <b>Agent:</b> \u2705 enabled\n")
		} else {
			b.WriteString("\u2022 <b>Agent:</b> disabled\n")
		}
		return b.String()
	}

	b.WriteString("\n<b>Platforms:</b>\n")
	if cfg.EnableX {
		b.WriteString("\u2022 X: \u2705 enabled\n")
		b.WriteString("  Token: " + redact(cfg.XUserBearerToken) + "\n")
		if cfg.XRefreshToken != "" {
			b.WriteString("  Auto-refresh: \u2705\n")
		}
	} else {
		b.WriteString("\u2022 X: disabled\n")
	}
	if cfg.EnableLinkedIn {
		b.WriteString("\u2022 LinkedIn: \u2705 enabled\n")
		b.WriteString("  Token: " + redact(cfg.LinkedInAccessToken) + "\n")
		if cfg.LinkedInAuthorURN != "" {
			b.WriteString("  Author URN: set\n")
		} else {
			b.WriteString("  Author URN: \u274c not set\n")
		}
		b.WriteString("  API version: " + cfg.LinkedInVersion + "\n")
	} else {
		b.WriteString("\u2022 LinkedIn: disabled\n")
	}

	if cfg.AgentWebhookURL != "" {
		b.WriteString("\n\u2022 <b>Agent:</b> \u2705 enabled\n")
	} else {
		b.WriteString("\n\u2022 <b>Agent:</b> disabled\n")
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

// tryRefreshXToken proactively refreshes the X access token using the stored
// refresh token and client credentials. Returns an updated Config on success.
func tryRefreshXToken(ctx context.Context, logger *log.Logger, cfg *Config) (Config, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	oauthCfg := x.OAuthConfig{ClientID: cfg.XClientID, ClientSecret: cfg.XClientSecret}

	tr, err := x.RefreshAccessToken(ctx, httpClient, oauthCfg, cfg.XRefreshToken)
	if err != nil {
		logger.Printf("X token refresh failed: %v", err)
		return *cfg, err
	}

	updated := *cfg
	updated.XUserBearerToken = tr.AccessToken
	if tr.RefreshToken != "" {
		updated.XRefreshToken = tr.RefreshToken
	}

	// Persist the refreshed tokens.
	stored, _, _ := store.Load(cfg.ConfigPath)
	stored.XUserBearerToken = updated.XUserBearerToken
	stored.XRefreshToken = updated.XRefreshToken
	if err := store.Save(cfg.ConfigPath, stored); err != nil {
		logger.Printf("failed to persist refreshed X token: %v", err)
	} else {
		logger.Printf("X token refreshed and saved")
	}
	return updated, nil
}

// isXAuthError detects X API authentication errors (expired/wrong token type).
func isXAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Unsupported Authentication") ||
		strings.Contains(s, "Application-Only") ||
		strings.Contains(s, "unsupported-authentication") ||
		strings.Contains(s, "401 Unauthorized")
}

const xAuthHint = "\U0001f511 Your X token has expired or is the wrong type (App-only). " +
	"Run /setup and use the OAuth flow (method 1) to get a fresh User Context token."

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// isStaleKeyboardButton detects leftover keyboard button text from a finished setup wizard.
func isStaleKeyboardButton(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	switch {
	case t == "back" || t == "⬅️ back" || t == "⬅ back" || t == "⬅️":
		return true
	case t == "yes (recommended)" || t == "no":
		return true
	case strings.HasPrefix(t, "1)") || strings.HasPrefix(t, "2)") || strings.HasPrefix(t, "3)"):
		return true
	case t == "use detected" || t == "✅ use detected":
		return true
	case t == "generate" || t == "skip" || t == "paste":
		return true
	}
	return false
}
