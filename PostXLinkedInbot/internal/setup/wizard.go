package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	stepXMethod         Step = "x_method"
	stepXClientID       Step = "x_client_id"
	stepXClientSecret   Step = "x_client_secret"
	stepXAuthCallback   Step = "x_auth_callback"
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
	ChatID       int64
	Step         Step
	History      []Step          // stack for back-navigation
	Draft        store.Config
	DetectedURN  string          // auto-detected LinkedIn URN from /userinfo
	DetectedName string          // auto-detected LinkedIn display name
	PKCE         x.PKCEChallenge // PKCE verifier for X OAuth flow
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
			LinkedInVersion: "202601",
		},
	}
}

// NewForX creates a wizard that skips straight to the X auth step.
func NewForX(chatID int64) *Wizard {
	return &Wizard{
		ChatID: chatID,
		Step:   stepXMethod,
		Draft: store.Config{
			Mode:            store.ModeDirect,
			EnableX:         true,
			EnableLinkedIn:  false,
			XAPIBaseURL:     "https://api.x.com",
			LinkedInVersion: "202601",
		},
	}
}

// NewForLinkedIn creates a wizard that skips straight to the LinkedIn auth step.
func NewForLinkedIn(chatID int64) *Wizard {
	return &Wizard{
		ChatID: chatID,
		Step:   stepLinkedInToken,
		Draft: store.Config{
			Mode:            store.ModeDirect,
			EnableX:         false,
			EnableLinkedIn:  true,
			XAPIBaseURL:     "https://api.x.com",
			LinkedInVersion: "202601",
		},
	}
}

// StartAtCurrentStep sends the prompt for the wizard's current step (used by platform-specific shortcuts).
func (w *Wizard) StartAtCurrentStep(tg *telegram.Client) {
	_, _, _ = w.reprompt(tg)
}

// PreloadDraft merges existing stored config into the draft so a platform-specific
// wizard doesn't wipe the other platform's tokens when it saves.
func (w *Wizard) PreloadDraft(stored store.Config) {
	// Preserve fields the shortcut wizard won't touch.
	if stored.AllowedChatID != 0 {
		w.Draft.AllowedChatID = stored.AllowedChatID
	}
	if stored.MaxImageBytes > 0 {
		w.Draft.MaxImageBytes = stored.MaxImageBytes
	}
	w.Draft.AgentWebhookURL = stored.AgentWebhookURL
	w.Draft.AgentSharedSecret = stored.AgentSharedSecret
	w.Draft.AgentSecretEnabled = stored.AgentSecretEnabled

	// Keep the other platform's credentials intact.
	if !w.Draft.EnableX {
		// LinkedIn-only wizard: preserve X settings.
		w.Draft.EnableX = stored.EnableX
		w.Draft.XUserBearerToken = stored.XUserBearerToken
		w.Draft.XRefreshToken = stored.XRefreshToken
		w.Draft.XClientID = stored.XClientID
		w.Draft.XClientSecret = stored.XClientSecret
		w.Draft.XAPIBaseURL = stored.XAPIBaseURL
	}
	if !w.Draft.EnableLinkedIn {
		// X-only wizard: preserve LinkedIn settings.
		w.Draft.EnableLinkedIn = stored.EnableLinkedIn
		w.Draft.LinkedInAccessToken = stored.LinkedInAccessToken
		w.Draft.LinkedInAuthorURN = stored.LinkedInAuthorURN
		w.Draft.LinkedInVersion = stored.LinkedInVersion
	}
}

// pushStep records current step in history and advances to the next step.
func (w *Wizard) pushStep(next Step) {
	w.History = append(w.History, w.Step)
	w.Step = next
}

// goBack pops the history stack and re-prompts the previous step.
func (w *Wizard) goBack(tg *telegram.Client) (bool, store.Config, error) {
	if len(w.History) == 0 {
		_, _ = tg.SendText(w.ChatID, "Already at the first step.")
		return false, store.Config{}, nil
	}
	prev := w.History[len(w.History)-1]
	w.History = w.History[:len(w.History)-1]
	w.Step = prev
	return w.reprompt(tg)
}

// reprompt re-sends the prompt for the current step.
func (w *Wizard) reprompt(tg *telegram.Client) (bool, store.Config, error) {
	switch w.Step {
	case stepLockToChat:
		w.promptStart(tg)
		return false, store.Config{}, nil
	case stepMode:
		return w.promptMode(tg)
	case stepPlatforms:
		return w.promptPlatforms(tg)
	case stepXMethod:
		return w.promptXMethod(tg)
	case stepXClientID:
		return w.promptXClientID(tg)
	case stepXClientSecret:
		return w.promptXClientSecret(tg)
	case stepXToken:
		return w.promptXToken(tg)
	case stepLinkedInToken:
		return w.promptLinkedInToken(tg)
	case stepLinkedInAuthor:
		return w.promptLinkedInAuthor(tg)
	case stepAgentEnable:
		return w.promptAgentEnable(tg)
	case stepAgentURL:
		return w.promptAgentURL(tg)
	case stepAgentSecret:
		return w.promptAgentSecret(tg)
	case stepN8NWebhookURL:
		return w.promptN8NURL(tg)
	case stepN8NSharedSecret:
		return w.promptN8NSecret(tg)
	default:
		return false, store.Config{}, nil
	}
}

// backButton creates a keyboard row with a back button.
func backRow() tgbotapi.KeyboardButton {
	return tgbotapi.NewKeyboardButton("\u2b05\ufe0f Back")
}

// isBack checks if user typed back.
func isBack(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return t == "back" || t == "\u2b05\ufe0f back" || t == "\u2b05 back" || t == "\u2b05\ufe0f"
}

func (w *Wizard) Start(tg *telegram.Client) {
	w.promptStart(tg)
}

func (w *Wizard) promptStart(tg *telegram.Client) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Yes (recommended)")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("No")),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, "\U0001f512 <b>Lock this bot to this chat?</b>\n\nThis prevents anyone else from using or configuring it.\n<i>Recommended: Yes</i>", kb)
}

func (w *Wizard) HandleText(ctx context.Context, tg *telegram.Client, text string) (done bool, cfg store.Config, err error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, store.Config{}, nil
	}

	// Global commands.
	switch strings.ToLower(text) {
	case "/cancel":
		w.Step = stepDone
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "Setup cancelled. Use /setup to try again.")
		return true, store.Config{}, errors.New("cancelled")
	}

	// Back navigation (works in any step except the very first).
	if isBack(text) {
		return w.goBack(tg)
	}

	switch w.Step {
	case stepLockToChat:
		if strings.HasPrefix(strings.ToLower(text), "y") {
			w.Draft.AllowedChatID = w.ChatID
		} else {
			w.Draft.AllowedChatID = 0
		}
		w.pushStep(stepMode)
		return w.promptMode(tg)

	case stepMode:
		switch normalizeChoice(text) {
		case "1", "direct":
			w.Draft.Mode = store.ModeDirect
			w.pushStep(stepPlatforms)
			return w.promptPlatforms(tg)
		case "2", "n8n":
			w.Draft.Mode = store.ModeN8N
			w.pushStep(stepN8NWebhookURL)
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
			w.pushStep(stepXMethod)
			return w.promptXMethod(tg)
		}
		if w.Draft.EnableLinkedIn {
			w.pushStep(stepLinkedInToken)
			return w.promptLinkedInToken(tg)
		}
		w.Step = stepDone
		_, _ = tg.SendTextRemoveKeyboard(w.ChatID, "No platform selected. Setup cancelled. Use /setup and pick at least one.")
		return true, store.Config{}, errors.New("no platform selected")

	case stepXMethod:
		switch normalizeChoice(text) {
		case "1", "oauth":
			w.pushStep(stepXClientID)
			return w.promptXClientID(tg)
		case "2", "paste":
			w.pushStep(stepXToken)
			return w.promptXToken(tg)
		default:
			_, _ = tg.SendText(w.ChatID, "Reply 1 (OAuth flow) or 2 (Paste token)")
			return false, store.Config{}, nil
		}

	case stepXClientID:
		w.Draft.XClientID = strings.TrimSpace(text)
		if len(w.Draft.XClientID) < 5 {
			_, _ = tg.SendText(w.ChatID, "That doesn't look like a Client ID. Find it in your X Developer Dashboard under OAuth 2.0 Keys.\nSend it again, or /cancel.")
			return false, store.Config{}, nil
		}
		w.pushStep(stepXClientSecret)
		return w.promptXClientSecret(tg)

	case stepXClientSecret:
		w.Draft.XClientSecret = strings.TrimSpace(text)
		if len(w.Draft.XClientSecret) < 5 {
			_, _ = tg.SendText(w.ChatID, "That doesn't look like a Client Secret. Find it in your X Developer Dashboard under OAuth 2.0 Keys.\nSend it again, or /cancel.")
			return false, store.Config{}, nil
		}
		// Generate PKCE and send the auth URL.
		pkce, perr := x.GeneratePKCE()
		if perr != nil {
			_, _ = tg.SendText(w.ChatID, "Internal error generating PKCE. Try /cancel and start again.")
			return false, store.Config{}, perr
		}
		w.PKCE = pkce
		authURL := x.BuildAuthURL(w.Draft.XClientID, pkce)
		_, _ = tg.SendHTML(w.ChatID, "\U0001f517 <b>Click this link to authorize your X account:</b>\n\n"+
			"<a href=\""+authURL+"\">\u2192 Authorize PostXLinkedIn on X</a>\n\n"+
			"\u2139\ufe0f After you authorize, the browser will redirect to a page that <b>won't load</b> \u2014 that's normal!\n\n"+
			"<b>Copy the entire URL</b> from your browser's address bar and paste it here.\n\n"+
			"It looks like:\n<code>https://127.0.0.1/callback?state=setup&amp;code=XXXX...</code>\n\n"+
			"Or send \u2b05\ufe0f <b>Back</b> to go back.")
		w.pushStep(stepXAuthCallback)
		return false, store.Config{}, nil

	case stepXAuthCallback:
		code, cerr := x.ExtractCodeFromCallback(text)
		if cerr != nil {
			_, _ = tg.SendHTML(w.ChatID, "\u274c Could not extract the authorization code.\n\nPaste the <b>full URL</b> from your browser (starts with <code>https://127.0.0.1/callback?...</code>), or \u2b05\ufe0f <b>Back</b>, or /cancel.")
			return false, store.Config{}, nil
		}
		_, _ = tg.SendText(w.ChatID, "Exchanging code for access token...")
		oauthCfg := x.OAuthConfig{ClientID: w.Draft.XClientID, ClientSecret: w.Draft.XClientSecret}
		tr, terr := x.ExchangeCode(ctx, &http.Client{Timeout: 15 * time.Second}, oauthCfg, code, w.PKCE)
		if terr != nil {
			_, _ = tg.SendHTML(w.ChatID, "\u274c <b>Token exchange failed:</b>\n<code>"+escapeHTML(terr.Error())+"</code>\n\nThe authorization code may have expired (they last ~30 seconds). Click the link above again, authorize, and paste the URL faster this time.\n\nOr \u2b05\ufe0f <b>Back</b>, or /cancel.")
			return false, store.Config{}, nil
		}
		w.Draft.XUserBearerToken = tr.AccessToken
		w.Draft.XRefreshToken = tr.RefreshToken
		// Verify the token works.
		vc := x.New(&http.Client{Timeout: 15 * time.Second}, w.Draft.XAPIBaseURL, tr.AccessToken)
		if me, verr := vc.Verify(ctx); verr != nil {
			_, _ = tg.SendHTML(w.ChatID, "\u274c <b>Token obtained but verification failed:</b>\n<code>"+escapeHTML(verr.Error())+"</code>\n\nTry \u2b05\ufe0f <b>Back</b> and try again, or /cancel.")
			return false, store.Config{}, nil
		} else {
			_, _ = tg.SendHTML(w.ChatID, fmt.Sprintf("\u2705 <b>X connected!</b> Logged in as <b>@%s</b> (%s)\n\n\U0001f504 Token auto-refresh is enabled.", me.Data.Username, me.Data.Name))
		}
		if w.Draft.EnableLinkedIn {
			w.pushStep(stepLinkedInToken)
			return w.promptLinkedInToken(tg)
		}
		w.pushStep(stepAgentEnable)
		return w.promptAgentEnable(tg)

	case stepXToken:
		w.Draft.XUserBearerToken = sanitizeToken(text)
		vc := x.New(&http.Client{Timeout: 15 * time.Second}, w.Draft.XAPIBaseURL, w.Draft.XUserBearerToken)
		if me, verr := vc.Verify(ctx); verr != nil {
			_, _ = tg.SendHTML(w.ChatID, "\u274c <b>X token check failed.</b>\n\nMake sure it's an OAuth 2.0 user access token with scope <code>users.read</code>.\n\nSend the token again, \u2b05\ufe0f <b>Back</b>, or /cancel.")
			return false, store.Config{}, nil
		} else {
			_, _ = tg.SendHTML(w.ChatID, fmt.Sprintf("\u2705 Verified! Logged in as <b>@%s</b> (%s)", me.Data.Username, me.Data.Name))
		}
		if w.Draft.EnableLinkedIn {
			w.pushStep(stepLinkedInToken)
			return w.promptLinkedInToken(tg)
		}
		w.pushStep(stepAgentEnable)
		return w.promptAgentEnable(tg)

	case stepLinkedInToken:
		w.Draft.LinkedInAccessToken = sanitizeToken(text)
		vc := linkedin.New(&http.Client{Timeout: 15 * time.Second}, w.Draft.LinkedInAccessToken, w.Draft.LinkedInVersion)
		ui, verr := vc.VerifyUserInfo(ctx)
		if verr != nil {
			_, _ = tg.SendHTML(w.ChatID, "\u274c <b>LinkedIn token check failed.</b>\n\nEnsure it's a valid access token with <code>openid</code> and <code>profile</code> scopes.\n\nSend it again, \u2b05\ufe0f <b>Back</b>, or /cancel.")
			return false, store.Config{}, nil
		}
		if ui.Sub != "" {
			w.DetectedURN = "urn:li:person:" + ui.Sub
			w.DetectedName = ui.Name
			_, _ = tg.SendHTML(w.ChatID, fmt.Sprintf("\u2705 Verified! Logged in as <b>%s</b>", ui.Name))
		} else {
			_, _ = tg.SendHTML(w.ChatID, "\u2705 Token verified!")
		}
		w.pushStep(stepLinkedInAuthor)
		return w.promptLinkedInAuthor(tg)

	case stepLinkedInAuthor:
		if w.DetectedURN != "" && (strings.HasPrefix(strings.ToLower(text), "use") || strings.HasPrefix(strings.ToLower(text), "\u2705") || normalizeChoice(text) == "me") {
			w.Draft.LinkedInAuthorURN = w.DetectedURN
		} else if normalizeChoice(text) == "me" {
			// No auto-detected URN; try GetMyPersonURN with own client.
			vc := linkedin.New(&http.Client{Timeout: 15 * time.Second}, w.Draft.LinkedInAccessToken, w.Draft.LinkedInVersion)
			urn, verr := vc.GetMyPersonURN(ctx)
			if verr != nil {
				_, _ = tg.SendHTML(w.ChatID, "\u274c Auto-detect failed. Paste your author URN (<code>urn:li:person:...</code> or <code>urn:li:organization:...</code>), \u2b05\ufe0f <b>Back</b>, or /cancel.")
				return false, store.Config{}, nil
			}
			w.Draft.LinkedInAuthorURN = urn
		} else {
			urn, ok := parseLinkedInAuthor(text)
			if !ok {
				_, _ = tg.SendHTML(w.ChatID, "\u274c Invalid author. Send:\n\u2022 <b>Use detected</b> (if shown)\n\u2022 <code>urn:li:person:...</code>\n\u2022 <code>urn:li:organization:...</code>\n\u2022 <code>person:123</code> or <code>org:123</code>\n\nSend again, \u2b05\ufe0f <b>Back</b>, or /cancel.")
				return false, store.Config{}, nil
			}
			w.Draft.LinkedInAuthorURN = urn
		}
		// Try initialize upload as a posting-permission check (non-destructive).
		vc := linkedin.New(&http.Client{Timeout: 15 * time.Second}, w.Draft.LinkedInAccessToken, w.Draft.LinkedInVersion)
		if _, _, verr := vc.InitializeImageUpload(ctx, w.Draft.LinkedInAuthorURN); verr != nil {
			_, _ = tg.SendHTML(w.ChatID, "\u274c <b>LinkedIn posting permission check failed.</b>\n\nCommon fix: your token needs <code>w_member_social</code> scope, and your LinkedIn app must have the \"Share on LinkedIn\" product approved.\n\nSend the author URN again if it was wrong, \u2b05\ufe0f <b>Back</b>, or /cancel.")
			return false, store.Config{}, nil
		}
		_, _ = tg.SendHTML(w.ChatID, "\u2705 LinkedIn posting permission confirmed!")

		w.pushStep(stepAgentEnable)
		return w.promptAgentEnable(tg)

	case stepAgentEnable:
		if strings.HasPrefix(strings.ToLower(text), "y") {
			w.pushStep(stepAgentURL)
			return w.promptAgentURL(tg)
		}
		w.Step = stepDone
		_, _ = tg.SendHTMLRemoveKeyboard(w.ChatID, "\u2705 <b>Setup complete!</b>\n\n\U0001f512 <b>Tip:</b> Delete the messages where you pasted tokens.\n\n\U0001f4f8 Send a photo with a caption to post!")
		return true, w.Draft, nil

	case stepAgentURL:
		u, perr := url.Parse(text)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			_, _ = tg.SendText(w.ChatID, "Invalid URL. Example: https://your-agent.example/process\nSend again, \u2b05\ufe0f Back, or /cancel.")
			return false, store.Config{}, nil
		}
		w.Draft.AgentWebhookURL = text
		w.pushStep(stepAgentSecret)
		return w.promptAgentSecret(tg)

	case stepAgentSecret:
		if normalizeChoice(text) == "generate" {
			sec, _ := generateSecret(32)
			w.Draft.AgentSharedSecret = sec
		} else if normalizeChoice(text) == "skip" {
			w.Draft.AgentSharedSecret = ""
		} else if strings.Contains(strings.ToLower(text), "paste") {
			_, _ = tg.SendText(w.ChatID, "Paste your secret now, or reply Generate or Skip.")
			return false, store.Config{}, nil
		} else {
			w.Draft.AgentSharedSecret = text
		}
		w.Draft.AgentSecretEnabled = w.Draft.AgentSharedSecret != ""
		w.Step = stepDone
		msg := "\u2705 <b>Setup complete!</b>\n\n\U0001f916 Agent webhook enabled \u2014 it will rewrite your caption before posting.\n\U0001f512 <b>Tip:</b> Delete the messages where you pasted tokens.\n\n\U0001f4f8 Send a photo with a caption to post!"
		if w.Draft.AgentSharedSecret != "" {
			msg += "\n\nYour agent secret:\n<code>" + w.Draft.AgentSharedSecret + "</code>"
		}
		_, _ = tg.SendHTMLRemoveKeyboard(w.ChatID, msg)
		return true, w.Draft, nil

	case stepN8NWebhookURL:
		u, perr := url.Parse(text)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			_, _ = tg.SendText(w.ChatID, "Invalid URL. Example: http://your-vps-ip:5678/webhook/postxlinkedin\nSend again, \u2b05\ufe0f Back, or /cancel.")
			return false, store.Config{}, nil
		}
		w.Draft.N8NWebhookURL = text
		w.pushStep(stepN8NSharedSecret)
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
		msg := "\u2705 <b>Setup complete!</b>\n\nIn your n8n workflow, verify header <code>X-PostXLinkedIn-Secret</code> matches your secret.\n\n\U0001f4f8 Send a photo with caption to post!"
		if w.Draft.N8NSharedSecret != "" {
			msg += "\n\nYour n8n secret:\n<code>" + w.Draft.N8NSharedSecret + "</code>"
		}
		_, _ = tg.SendHTMLRemoveKeyboard(w.ChatID, msg)
		return true, w.Draft, nil

	default:
		return false, store.Config{}, nil
	}
}

func (w *Wizard) promptMode(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("1) Direct (recommended)")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("2) n8n webhook")),
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, "\U0001f4e1 <b>Choose posting mode:</b>\n\n<b>1) Direct</b> \u2014 bot posts to X + LinkedIn via their APIs\n<b>2) n8n webhook</b> \u2014 bot sends data to n8n, which handles posting\n\n<i>Direct is simpler. Use n8n only if you have an n8n workflow set up.</i>", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptPlatforms(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("1) X + LinkedIn")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("2) X only")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("3) LinkedIn only")),
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, "\U0001f3af <b>Which platforms?</b>\n\n1) X + LinkedIn (both)\n2) X only\n3) LinkedIn only", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptXMethod(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("1) OAuth flow (easiest)")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("2) Paste token directly")),
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	msg := "\U0001f426 <b>X (Twitter) Setup</b>\n\n" +
		"<b>1) OAuth flow</b> (easiest) \u2014 I'll handle everything. You just:\n" +
		"   \u2022 Paste your Client ID + Secret from the <a href=\"https://developer.x.com/en/portal/dashboard\">X Dashboard</a>\n" +
		"   \u2022 Click a link to authorize\n" +
		"   \u2022 Paste the callback URL back\n" +
		"   \u2022 Done! Uses PKCE S256 with token auto-refresh.\n\n" +
		"<b>2) Paste token directly</b> \u2014 if you already have a Bearer token.\n\n" +
		"<i>Don't have a Developer Account yet? <a href=\"https://developer.x.com/en/portal/petition/essential/basic-info\">Sign up free</a>, then create a Project + App.</i>"
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptXClientID(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	msg := "\U0001f511 <b>X Client ID</b>\n\n" +
		"<b>Find it here:</b>\n" +
		"\u2192 <a href=\"https://developer.x.com/en/portal/dashboard\">X Developer Dashboard</a>\n" +
		"\u2192 Click your App \u2192 <b>Keys and tokens</b>\n" +
		"\u2192 Scroll to <b>OAuth 2.0 Keys</b>\n" +
		"\u2192 Copy the <b>Client ID</b>\n\n" +
		"\u26a0\ufe0f <b>First time?</b> Click <b>\"Edit settings\"</b> next to OAuth 2.0 and set:\n" +
		"   \u2022 Type: <b>Web App</b> (confidential client)\n" +
		"   \u2022 Callback URL: <code>https://127.0.0.1/callback</code>\n" +
		"   \u2022 Website: any URL \u2192 Save\n\n" +
		"\U0001f4d6 <a href=\"https://docs.x.com/resources/fundamentals/authentication/oauth-2-0/authorization-code\">X OAuth 2.0 Docs</a>\n\n" +
		"Paste your Client ID below, \u2b05\ufe0f Back, or /cancel."
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptXClientSecret(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	msg := "\U0001f510 <b>X Client Secret</b>\n\n" +
		"In the same <b>Keys and tokens</b> page:\n" +
		"\u2192 Under <b>OAuth 2.0 Keys</b> \u2192 <b>Client Secret</b>\n" +
		"\u2192 If it says \"already generated\", click <b>Regenerate</b> to see it\n\n" +
		"Paste your Client Secret below, \u2b05\ufe0f Back, or /cancel."
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptXToken(tg *telegram.Client) (bool, store.Config, error) {
	msg := "\U0001f511 <b>X (Twitter) \u2014 Get Your Access Token</b>\n" +
		"\n" +
		"<b>Step 1: Create a Developer Account</b>\n" +
		"\u2192 <a href=\"https://developer.x.com/en/portal/petition/essential/basic-info\">Sign up at developer.x.com</a> (Free tier works)\n" +
		"\n" +
		"<b>Step 2: Create a Project &amp; App</b>\n" +
		"\u2192 <a href=\"https://developer.x.com/en/portal/dashboard\">Developer Dashboard</a> \u2192 \"Create Project\" \u2192 name it \u2192 \"Create App\"\n" +
		"\n" +
		"<b>Step 3: Set up OAuth 2.0</b>\n" +
		"\u2192 App Settings \u2192 \"User authentication settings\" \u2192 Edit\n" +
		"\u2192 Permissions: <b>Read and write</b>\n" +
		"\u2192 Type: <b>Web App</b> (confidential client)\n" +
		"\u2192 Callback URL: <code>https://127.0.0.1/callback</code>\n" +
		"\u2192 Website URL: any URL \u2192 Save\n" +
		"\u2192 Copy your <b>Client ID</b>\n" +
		"\n" +
		"<b>Step 4: Get token via Postman (easiest)</b>\n" +
		"\u2192 <a href=\"https://www.postman.com/downloads/\">Download Postman</a> (free)\n" +
		"\u2192 New Request \u2192 Auth tab \u2192 Type: OAuth 2.0\n" +
		"\u2192 Auth URL: <code>https://x.com/i/oauth2/authorize</code>\n" +
		"\u2192 Token URL: <code>https://api.x.com/2/oauth2/token</code>\n" +
		"\u2192 Client ID: yours\n" +
		"\u2192 Scope: <code>tweet.read tweet.write users.read media.write offline.access</code>\n" +
		"\u2192 Code Challenge: <b>SHA-256</b>\n" +
		"\u2192 Click \"Get New Access Token\" \u2192 Authorize \u2192 Copy token\n" +
		"\n" +
		"\U0001f4d6 <a href=\"https://docs.x.com/resources/fundamentals/authentication/oauth-2-0/authorization-code\">X OAuth 2.0 PKCE Docs</a>\n" +
		"\n" +
		"Paste the token below, \u2b05\ufe0f Back, or /cancel."
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptLinkedInToken(tg *telegram.Client) (bool, store.Config, error) {
	msg := "\U0001f511 <b>LinkedIn \u2014 Get Your Access Token</b>\n" +
		"\n" +
		"LinkedIn has a built-in token generator \u2014 no extra tools needed!\n" +
		"\n" +
		"<b>Step 1: Create a LinkedIn App</b>\n" +
		"\u2192 <a href=\"https://www.linkedin.com/developers/apps/new\">Create App</a>\n" +
		"\u2192 App name: anything (e.g. \"MyPostBot\")\n" +
		"\u2192 LinkedIn Page: pick yours or <a href=\"https://www.linkedin.com/company/setup/new/\">create one</a>\n" +
		"\u2192 Upload any logo \u2192 Create app\n" +
		"\n" +
		"<b>Step 2: Request API Access</b>\n" +
		"\u2192 In your app \u2192 <b>Products</b> tab\n" +
		"\u2192 Request \"<b>Share on LinkedIn</b>\" and \"<b>Sign In with LinkedIn using OpenID Connect</b>\"\n" +
		"\u2192 Approval is usually instant\n" +
		"\n" +
		"<b>Step 3: Generate Token</b>\n" +
		"\u2192 <a href=\"https://www.linkedin.com/developers/tools/oauth/token-generator\">LinkedIn Token Generator</a>\n" +
		"\u2192 Select your app\n" +
		"\u2192 Check: <code>openid</code>, <code>profile</code>, <code>w_member_social</code>\n" +
		"\u2192 Click <b>Request access token</b> \u2192 Authorize \u2192 Copy token\n" +
		"\n" +
		"\U0001f4d6 <a href=\"https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api\">LinkedIn Posts API Docs</a>\n" +
		"\U0001f4d6 <a href=\"https://learn.microsoft.com/en-us/linkedin/marketing/versioning\">API Versioning (latest: 202601)</a>\n" +
		"\n" +
		"\u26a0\ufe0f Tokens expire in ~60 days. Run /setup again to refresh.\n" +
		"\n" +
		"Paste the token below, \u2b05\ufe0f Back, or /cancel."
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptLinkedInAuthor(tg *telegram.Client) (bool, store.Config, error) {
	if w.DetectedURN != "" {
		name := w.DetectedName
		if name == "" {
			name = "your account"
		}
		kb := tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("\u2705 Use detected")),
			tgbotapi.NewKeyboardButtonRow(backRow()),
		)
		kb.OneTimeKeyboard = true
		kb.ResizeKeyboard = true
		msg := fmt.Sprintf("\U0001f194 <b>LinkedIn Author URN</b>\n"+
			"\n"+
			"\u2705 Auto-detected your profile:\n"+
			"<b>%s</b>\n"+
			"URN: <code>%s</code>\n"+
			"\n"+
			"Tap <b>\"\u2705 Use detected\"</b> to confirm, or paste a different URN.\n"+
			"\n"+
			"For organization pages: <code>urn:li:organization:YOUR_ORG_ID</code>\n"+
			"\u2192 Find Org ID in your <a href=\"https://www.linkedin.com/company/\">Company Page</a> admin URL\n"+
			"\n"+
			"\U0001f4d6 <a href=\"https://learn.microsoft.com/en-us/linkedin/shared/api-guide/concepts/urns\">LinkedIn URN Docs</a>",
			name, w.DetectedURN)
		_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
		return false, store.Config{}, nil
	}

	msg := "\U0001f194 <b>LinkedIn Author URN</b>\n" +
		"\n" +
		"The author URN identifies who posts. Format:\n" +
		"\u2022 Personal: <code>urn:li:person:YOUR_ID</code>\n" +
		"\u2022 Organization: <code>urn:li:organization:YOUR_ID</code>\n" +
		"\n" +
		"<b>Easiest way:</b> Reply <b>me</b> to auto-detect your person URN.\n" +
		"\n" +
		"Or find your ID manually:\n" +
		"\u2192 Open your LinkedIn profile in a browser\n" +
		"\u2192 Check the URL or page source for your member ID\n" +
		"\n" +
		"Shortcuts: <code>person:123</code> or <code>org:123</code>\n" +
		"\n" +
		"\U0001f4d6 <a href=\"https://learn.microsoft.com/en-us/linkedin/shared/api-guide/concepts/urns\">LinkedIn URN Docs</a>\n" +
		"\n" +
		"Paste your URN (or reply <b>me</b>), \u2b05\ufe0f Back, or /cancel."
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptAgentEnable(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Yes")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("No (skip)")),
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, "\U0001f916 <b>AI Agent Webhook (optional)</b>\n\nAn agent can rewrite your caption (add hashtags, formatting, etc.) before posting.\n\nSkip this if you don't use an AI agent.\n\nEnable agent webhook?", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptAgentURL(tg *telegram.Client) (bool, store.Config, error) {
	msg := "\U0001f916 <b>Agent Webhook URL</b>\n" +
		"\n" +
		"Send the URL of your AI agent webhook.\n" +
		"\n" +
		"It must accept a JSON POST:\n" +
		"<code>{\"caption\": \"...\", \"targets\": [\"x\",\"linkedin\"]}</code>\n" +
		"\n" +
		"And return:\n" +
		"<code>{\"ok\": true, \"caption\": \"rewritten text...\"}</code>\n" +
		"\n" +
		"Paste the URL below, \u2b05\ufe0f Back, or /cancel."
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptAgentSecret(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Generate")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Skip")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("I'll paste my own")),
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, "\U0001f510 <b>Agent Shared Secret</b>\n\nProtects your webhook from unauthorized calls.\n\n\u2022 <b>Generate</b> \u2014 auto-create a secure secret\n\u2022 <b>Skip</b> \u2014 no secret (not recommended)\n\u2022 Or paste your own secret", kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptN8NURL(tg *telegram.Client) (bool, store.Config, error) {
	msg := "\U0001f517 <b>n8n Webhook URL</b>\n" +
		"\n" +
		"Send the webhook URL from your n8n workflow.\n" +
		"\n" +
		"Example: <code>http://your-vps:5678/webhook/postxlinkedin</code>\n" +
		"\n" +
		"\U0001f4d6 <a href=\"https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.webhook/\">n8n Webhook Docs</a>\n" +
		"\n" +
		"Paste the URL below, \u2b05\ufe0f Back, or /cancel."
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, msg, kb)
	return false, store.Config{}, nil
}

func (w *Wizard) promptN8NSecret(tg *telegram.Client) (bool, store.Config, error) {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Generate")),
		tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("I'll paste my own")),
		tgbotapi.NewKeyboardButtonRow(backRow()),
	)
	kb.OneTimeKeyboard = true
	kb.ResizeKeyboard = true
	_, _ = tg.SendHTMLWithKeyboard(w.ChatID, "\U0001f510 <b>n8n Shared Secret</b>\n\nSecures the webhook. In your n8n workflow, verify the header <code>X-PostXLinkedIn-Secret</code> matches this value.\n\n\u2022 <b>Generate</b> \u2014 auto-create a secure secret\n\u2022 Or paste your own", kb)
	return false, store.Config{}, nil
}

func normalizeChoice(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "/")
	// Button texts look like "1) Direct (recommended)" — extract leading digit.
	if len(s) >= 2 && s[0] >= '1' && s[0] <= '9' && s[1] == ')' {
		return string(s[0])
	}
	s = strings.TrimSuffix(s, ")")
	return s
}

func sanitizeToken(s string) string {
	s = strings.TrimSpace(s)
	// Users commonly paste "Bearer <token>" from docs/tools.
	if strings.HasPrefix(strings.ToLower(s), "bearer ") {
		s = strings.TrimSpace(s[len("bearer "):])
	}
	return s
}

func parseLinkedInAuthor(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "urn:li:") {
		return s, true
	}
	// Accept shorthand forms: person:123 / org:123 / organization:123
	if strings.HasPrefix(low, "person:") {
		id := strings.TrimSpace(s[len("person:"):])
		if id == "" {
			return "", false
		}
		return "urn:li:person:" + id, true
	}
	if strings.HasPrefix(low, "org:") {
		id := strings.TrimSpace(s[len("org:"):])
		if id == "" {
			return "", false
		}
		return "urn:li:organization:" + id, true
	}
	if strings.HasPrefix(low, "organization:") {
		id := strings.TrimSpace(s[len("organization:"):])
		if id == "" {
			return "", false
		}
		return "urn:li:organization:" + id, true
	}
	return "", false
}

func generateSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
