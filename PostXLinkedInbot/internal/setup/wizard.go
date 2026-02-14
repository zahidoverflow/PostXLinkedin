package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/linkedin"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/store"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/telegram"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/x"
)

type Step string

const (
	stepLockToChat      Step = "lock_to_chat"
	stepMode            Step = "mode"
	stepPlatforms       Step = "platforms"
	stepXToken          Step = "x_token"
	stepLinkedInToken   Step = "li_token"
	stepLinkedInAuthor  Step = "li_author"
	stepAgentEnable     Step = "agent_enable"
	stepAgentURL        Step = "agent_url"
	stepAgentSecret     Step = "agent_secret"
	stepN8NWebhookURL   Step = "n8n_url"
	stepN8NSharedSecret Step = "n8n_secret"
	stepDone            Step = "done"
)

type Wizard struct {
	ChatID int64
	Step   Step
	Draft  store.Config
}

func New(chatID int64) *Wizard {
	return &Wizard{
		ChatID: chatID,
		Step:   stepLockToChat,
		Draft: store.Config{
			Mode:            store.ModeDirect,
			EnableX:         true,
			EnableLinkedIn:  true,
			XAPIBaseURL:     "https://api.x.com",
			LinkedInVersion: "202404",
		},
	}
}

func (w *Wizard) Start(tg *telegram.Client) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Yes (recommended)")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("No")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendTextWithKeyboard(w.ChatID, "Setup: lock this bot to this chat only?\n\nThis prevents anyone else from configuring or using it.\nReply: Yes (recommended) or No", kb)
}

func (w *Wizard) HandleText(ctx context.Context, tg *telegram.Client, xClient *x.Client, liClient *linkedin.Client, text string) (done bool, cfg store.Config, err error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, store.Config{}, nil
	}
	switch strings.ToLower(text) {
	case "/cancel":
		w.Step = stepDone
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Setup cancelled. Use /setup to try again.")
		return true, store.Config{}, errors.New("cancelled")
	}

	switch w.Step {
	case stepLockToChat:
		if strings.HasPrefix(strings.ToLower(text), "y") {
			w.Draft.AllowedChatID = w.ChatID
		} else {
			w.Draft.AllowedChatID = 0
		}
		w.Step = stepMode
		return w.promptMode(tg)

	case stepMode:
		switch normalizeChoice(text) {
		case "1", "direct":
			w.Draft.Mode = store.ModeDirect
			w.Step = stepPlatforms
			return w.promptPlatforms(tg)
		case "2", "n8n":
			w.Draft.Mode = store.ModeN8N
			w.Step = stepN8NWebhookURL
			return w.promptN8NURL(tg)
		default:
			_, _ = tg.SendText(w.ChatID, "Reply with: 1 (Direct) or 2 (n8n)")
			return false, store.Config{}, nil
		}

	case stepPlatforms:
		switch normalizeChoice(text) {
		case "1", "x+linkedin":
			w.Draft.EnableX = true
			w.Draft.EnableLinkedIn = true
		case "2", "x":
			w.Draft.EnableX = true
			w.Draft.EnableLinkedIn = false
		case "3", "linkedin":
			w.Draft.EnableX = false
			w.Draft.EnableLinkedIn = true
		default:
			_, _ = tg.SendText(w.ChatID, "Reply with: 1 (X + LinkedIn), 2 (X only), or 3 (LinkedIn only)")
			return false, store.Config{}, nil
		}

		if w.Draft.EnableX {
			w.Step = stepXToken
			return w.promptXToken(tg)
		}
		if w.Draft.EnableLinkedIn {
			w.Step = stepLinkedInToken
			return w.promptLinkedInToken(tg)
		}
		w.Step = stepDone
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "No platform selected. Setup cancelled. Use /setup and pick at least one.")
		return true, store.Config{}, errors.New("no platform selected")

	case stepXToken:
		w.Draft.XUserBearerToken = text
		if xClient != nil {
			if _, verr := xClient.Verify(ctx); verr != nil {
				_, _ = tg.SendText(w.ChatID, "X token check failed. Make sure it's an OAuth2 user access token with scope `users.read`.\nSend the token again, or /cancel.")
				return false, store.Config{}, nil
			}
		}
		if w.Draft.EnableLinkedIn {
			w.Step = stepLinkedInToken
			return w.promptLinkedInToken(tg)
		}
		w.Step = stepAgentEnable
		return w.promptAgentEnable(tg)

	case stepLinkedInToken:
		w.Draft.LinkedInAccessToken = text
		if liClient != nil {
			if _, verr := liClient.VerifyUserInfo(ctx); verr != nil {
				_, _ = tg.SendText(w.ChatID, "LinkedIn token check failed. Ensure it's a valid access token.\nSend it again, or /cancel.")
				return false, store.Config{}, nil
			}
		}
		w.Step = stepLinkedInAuthor
		return w.promptLinkedInAuthor(tg)

	case stepLinkedInAuthor:
		w.Draft.LinkedInAuthorURN = text
		if !strings.HasPrefix(w.Draft.LinkedInAuthorURN, "urn:li:") {
			_, _ = tg.SendText(w.ChatID, "That doesn't look like a LinkedIn URN. Example: urn:li:person:123...\nSend it again, or /cancel.")
			return false, store.Config{}, nil
		}
		// Try initialize upload as a posting-permission check (non-destructive).
		if liClient != nil {
			if _, _, verr := liClient.InitializeImageUpload(ctx, w.Draft.LinkedInAuthorURN); verr != nil {
				_, _ = tg.SendText(w.ChatID, "LinkedIn posting permission check failed.\nCommon fix: your token needs `w_member_social` (and/or `w_organization_social`), and your app must be allowed to post.\nSend the author URN again if it was wrong, or /cancel.")
				return false, store.Config{}, nil
			}
		}

		w.Step = stepAgentEnable
		return w.promptAgentEnable(tg)

	case stepAgentEnable:
		if strings.HasPrefix(strings.ToLower(text), "y") {
			w.Step = stepAgentURL
			return w.promptAgentURL(tg)
		}
		w.Step = stepDone
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Setup saved.\n\nTip: delete the messages where you pasted tokens.\nNow send a photo with caption to post.")
		return true, w.Draft, nil

	case stepAgentURL:
		u, perr := url.Parse(text)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			_, _ = tg.SendText(w.ChatID, "Invalid URL. Example: https://your-agent.example/process\nSend again, or /cancel.")
			return false, store.Config{}, nil
		}
		w.Draft.AgentWebhookURL = text
		w.Step = stepAgentSecret
		return w.promptAgentSecret(tg)

	case stepAgentSecret:
		if normalizeChoice(text) == "generate" {
			sec, _ := generateSecret(32)
			w.Draft.AgentSharedSecret = sec
		} else if normalizeChoice(text) == "skip" {
			w.Draft.AgentSharedSecret = ""
		} else if strings.Contains(strings.ToLower(text), "paste") {
			_, _ = tg.SendText(w.ChatID, "Paste your secret now, or reply Generate, or send empty to skip.")
			return false, store.Config{}, nil
		} else {
			w.Draft.AgentSharedSecret = text
		}
		w.Draft.AgentSecretEnabled = w.Draft.AgentSharedSecret != ""
		w.Step = stepDone
		msg := "Setup saved.\n\nIf you enabled an agent webhook, it can rewrite your caption before posting.\nTip: delete the messages where you pasted tokens.\nNow send a photo with caption to post."
		if w.Draft.AgentSharedSecret != "" {
			msg += "\n\nYour agent secret:\n" + w.Draft.AgentSharedSecret
		}
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, msg)
		return true, w.Draft, nil

	case stepN8NWebhookURL:
		u, perr := url.Parse(text)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			_, _ = tg.SendText(w.ChatID, "Invalid URL. Example: http://your-vps-ip:5678/webhook/postxlinkedin\nSend again, or /cancel.")
			return false, store.Config{}, nil
		}
		w.Draft.N8NWebhookURL = text
		w.Step = stepN8NSharedSecret
		return w.promptN8NSecret(tg)

	case stepN8NSharedSecret:
		if normalizeChoice(text) == "generate" {
			sec, _ := generateSecret(32)
			w.Draft.N8NSharedSecret = sec
		} else if strings.Contains(strings.ToLower(text), "paste") {
			_, _ = tg.SendText(w.ChatID, "Paste your secret now, or reply Generate.")
			return false, store.Config{}, nil
		} else {
			w.Draft.N8NSharedSecret = text
		}
		w.Draft.N8NSecretEnabled = w.Draft.N8NSharedSecret != ""
		w.Step = stepDone
		msg := "Setup saved.\n\nIn n8n, verify header X-PostXLinkedIn-Secret equals your secret.\nThen send a photo with caption to post."
		if w.Draft.N8NSharedSecret != "" {
			msg += "\n\nYour n8n secret:\n" + w.Draft.N8NSharedSecret
		}
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, msg)
		return true, w.Draft, nil

	default:
		return false, store.Config{}, nil
	}
}

func (w *Wizard) promptMode(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("1) Direct (recommended)")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("2) n8n webhook")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendTextWithKeyboard(w.ChatID, "Choose posting mode:\n\n1) Direct (bot posts to X + LinkedIn)\n2) n8n webhook (bot calls n8n, n8n posts)\n\nReply 1 or 2.", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptPlatforms(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("1) X + LinkedIn")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("2) X only")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("3) LinkedIn only")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendTextWithKeyboard(w.ChatID, "Which platforms should I post to?\n\nReply 1, 2, or 3.", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptXToken(tg *telegram.Client) (bool, store.Config, error) {
	// Keep instructions short and actionable.
	_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Send your X OAuth2 user access token (Bearer).\n\nMinimum scope to validate: users.read\nPosting needs: tweet.write\nMedia upload needs: media.write\n\nPaste the token now, or /cancel.")
	return false, store.Config{}, nil
}

func (w *Wizard) promptLinkedInToken(tg *telegram.Client) (bool, store.Config, error) {
	_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Send your LinkedIn access token.\n\nPosting commonly needs w_member_social (and w_organization_social for org pages).\n\nPaste the token now, or /cancel.")
	return false, store.Config{}, nil
}

func (w *Wizard) promptLinkedInAuthor(tg *telegram.Client) (bool, store.Config, error) {
	_, _ = tg.SendText(w.ChatID, "Send your LinkedIn author URN.\nExamples:\n- urn:li:person:123...\n- urn:li:organization:123...\n\nPaste it now, or /cancel.")
	return false, store.Config{}, nil
}

func (w *Wizard) promptAgentEnable(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Yes")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("No (skip)")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendTextWithKeyboard(w.ChatID, "Optional: enable an AI agent webhook to rewrite/format your caption before posting?\n\nIf you are not using an agent, reply No.\nReply Yes or No.", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptAgentURL(tg *telegram.Client) (bool, store.Config, error) {
	_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Send your agent webhook URL.\n\nIt must accept JSON {caption, targets} and return {ok:true, caption:\"...\"}.\n\nPaste it now, or /cancel.")
	return false, store.Config{}, nil
}

func (w *Wizard) promptAgentSecret(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Generate")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Skip")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("I'll paste my own")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendTextWithKeyboard(w.ChatID, "Agent shared secret (recommended).\n\nReply Generate to generate one, Skip to disable the secret, or paste your own.", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptN8NURL(tg *telegram.Client) (bool, store.Config, error) {
	_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Send your n8n webhook URL.\nExample: http://your-vps-ip:5678/webhook/postxlinkedin\n\nPaste it now, or /cancel.")
	return false, store.Config{}, nil
}

func (w *Wizard) promptN8NSecret(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Generate")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("I'll paste my own")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendTextWithKeyboard(w.ChatID, "n8n shared secret (recommended).\n\nReply \"Generate\" to generate one, or paste your own secret.", kb)
	return false, store.Config{}, nil
}

func normalizeChoice(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, ")")
	return s
}

func generateSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
