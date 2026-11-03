package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/agent"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/linkedin"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/opengraph"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/setup"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/store"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/telegram"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/x"
)

type postQueue struct {
	mu     sync.Mutex
	items  [][]*tgbotapi.Message
	notify chan struct{}
}

func newPostQueue() *postQueue {
	return &postQueue{
		notify: make(chan struct{}, 1),
	}
}

func (q *postQueue) Enqueue(msgs []*tgbotapi.Message) {
	if len(msgs) == 0 {
		return
	}
	q.mu.Lock()
	q.items = append(q.items, msgs)
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *postQueue) Dequeue(ctx context.Context) ([]*tgbotapi.Message, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			item := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			hasMore := len(q.items) > 0
			q.mu.Unlock()
			if hasMore {
				select {
				case q.notify <- struct{}{}:
				default:
				}
			}
			return item, true
		}
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, false
		case <-q.notify:
		}
	}
}

func (q *postQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

type mediaGroupBatch struct {
	messages []*tgbotapi.Message
	timer    *time.Timer
	chatID   int64
}

type mediaGroupCollector struct {
	mu                sync.Mutex
	batches           map[string]*mediaGroupBatch
	activeGroupByChat map[int64]string
	pendingComments   map[int64]*mediaGroupBatch
	ready             chan []*tgbotapi.Message
	queue             *postQueue
	timeout           time.Duration
}

func newMediaGroupCollector(ready chan []*tgbotapi.Message, timeout time.Duration) *mediaGroupCollector {
	return &mediaGroupCollector{
		batches:           make(map[string]*mediaGroupBatch),
		activeGroupByChat: make(map[int64]string),
		pendingComments:   make(map[int64]*mediaGroupBatch),
		ready:             ready,
		timeout:           timeout,
	}
}

func newMediaGroupCollectorWithQueue(queue *postQueue, timeout time.Duration) *mediaGroupCollector {
	return &mediaGroupCollector{
		batches:           make(map[string]*mediaGroupBatch),
		activeGroupByChat: make(map[int64]string),
		pendingComments:   make(map[int64]*mediaGroupBatch),
		queue:             queue,
		timeout:           timeout,
	}
}

func (c *mediaGroupCollector) emitLocked(msgs []*tgbotapi.Message) {
	if len(msgs) == 0 {
		return
	}
	if c.queue != nil {
		c.queue.Enqueue(msgs)
	}
	if c.ready != nil {
		c.ready <- msgs
	}
}

func (c *mediaGroupCollector) flushActiveGroupLocked(chatID int64) {
	prevID, ok := c.activeGroupByChat[chatID]
	if !ok || prevID == "" {
		return
	}
	delete(c.activeGroupByChat, chatID)
	if b, exists := c.batches[prevID]; exists {
		b.timer.Stop()
		delete(c.batches, prevID)
		c.emitLocked(b.messages)
	}
}

func isMessageForwarded(m *tgbotapi.Message) bool {
	if m == nil {
		return false
	}
	return m.ForwardDate != 0 || m.ForwardFrom != nil || m.ForwardFromChat != nil || m.ForwardSenderName != ""
}

func (c *mediaGroupCollector) Add(msg *tgbotapi.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var chatID int64
	if msg.Chat != nil {
		chatID = msg.Chat.ID
	}

	// If a different message or album arrives in the same chat while an album is pending,
	// flush the pending album first so strict FIFO forwarding order is preserved.
	if prevID := c.activeGroupByChat[chatID]; prevID != "" && prevID != msg.MediaGroupID {
		c.flushActiveGroupLocked(chatID)
	}

	// 1. Media Group (album) message
	if id := msg.MediaGroupID; id != "" {
		if b, ok := c.batches[id]; ok {
			b.messages = append(b.messages, msg)
			b.timer.Reset(c.timeout)
			return
		}

		var initial []*tgbotapi.Message
		if pc, hasPending := c.pendingComments[chatID]; hasPending {
			pc.timer.Stop()
			delete(c.pendingComments, chatID)
			initial = append(initial, pc.messages...)
		}
		initial = append(initial, msg)

		b := &mediaGroupBatch{
			messages: initial,
			chatID:   chatID,
		}
		b.timer = time.AfterFunc(c.timeout, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.batches[id] != b {
				return
			}
			msgs := b.messages
			delete(c.batches, id)
			if c.activeGroupByChat[chatID] == id {
				delete(c.activeGroupByChat, chatID)
			}
			c.emitLocked(msgs)
		})
		c.batches[id] = b
		c.activeGroupByChat[chatID] = id
		return
	}

	// 2. Single media message (Document/PDF, Photo, Video, etc.)
	if ExtractInboundMedia(msg) != nil {
		if pc, hasPending := c.pendingComments[chatID]; hasPending {
			pc.timer.Stop()
			delete(c.pendingComments, chatID)
			msgs := append(pc.messages, msg)
			c.emitLocked(msgs)
			return
		}
		c.emitLocked([]*tgbotapi.Message{msg})
		return
	}

	// 3. Forwarded text-only message
	if isMessageForwarded(msg) {
		if pc, hasPending := c.pendingComments[chatID]; hasPending {
			pc.timer.Stop()
			delete(c.pendingComments, chatID)
			msgs := append(pc.messages, msg)
			c.emitLocked(msgs)
			return
		}
		c.emitLocked([]*tgbotapi.Message{msg})
		return
	}

	// 4. Non-forwarded plain text message: buffer briefly in case it is the comment
	// preceding a forwarded media/document ("Forward with comment" in Telegram sends
	// the user's comment as a separate text message immediately before the forwarded message).
	if pc, hasPending := c.pendingComments[chatID]; hasPending {
		pc.timer.Stop()
		delete(c.pendingComments, chatID)
		c.emitLocked(pc.messages)
	}

	b := &mediaGroupBatch{
		messages: []*tgbotapi.Message{msg},
		chatID:   chatID,
	}
	b.timer = time.AfterFunc(c.timeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.pendingComments[chatID] != b {
			return
		}
		msgs := b.messages
		delete(c.pendingComments, chatID)
		c.emitLocked(msgs)
	})
	c.pendingComments[chatID] = b
}

func (c *mediaGroupCollector) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, b := range c.batches {
		b.timer.Stop()
		delete(c.batches, id)
	}
	for chatID := range c.activeGroupByChat {
		delete(c.activeGroupByChat, chatID)
	}
	for chatID, b := range c.pendingComments {
		b.timer.Stop()
		delete(c.pendingComments, chatID)
	}
}

func Run(ctx context.Context, logger *log.Logger, pollTimeout time.Duration) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	rt := Runtime{PollTimeout: pollTimeout}

	botHTTPClient := &http.Client{Timeout: 90 * time.Second}
	bot, err := tgbotapi.NewBotAPIWithClient(cfg.TelegramBotToken, tgbotapi.APIEndpoint, botHTTPClient)
	if err != nil {
		return fmt.Errorf("telegram init: %w", err)
	}
	bot.Debug = cfg.Debug

	httpClient := &http.Client{Timeout: 180 * time.Second}
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

	// Setup wizard sessions (per-chat) and shared config protected by stateMu.
	var stateMu sync.Mutex
	sessions := map[int64]*setup.Wizard{}

	updateCfg := tgbotapi.NewUpdate(0)
	updateCfg.Timeout = int(rt.PollTimeout.Seconds())
	updates := bot.GetUpdatesChan(updateCfg)

	queue := newPostQueue()
	mgCollector := newMediaGroupCollectorWithQueue(queue, 1200*time.Millisecond)
	defer mgCollector.StopAll()

	// Dedicated sequential worker: processes queued posts strictly one by one in FIFO order
	// while the main loop continues ingesting incoming Telegram updates without blocking.
	var workerWG sync.WaitGroup
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		for {
			msgs, ok := queue.Dequeue(ctx)
			if !ok {
				return
			}
			remaining := queue.Len()
			if remaining > 0 {
				logger.Printf("processing post batch (%d message(s), %d more post(s) waiting in queue)...", len(msgs), remaining)
			}
			stateMu.Lock()
			err := handleMessages(ctx, logger, &cfg, tg, sessions, msgs)
			stateMu.Unlock()
			if err != nil {
				logger.Printf("handleMessages error: %v", err)
			}
			if queue.Len() > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
	}()
	defer workerWG.Wait()

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

			stateMu.Lock()
			inSetup := sessions[chatID] != nil
			stateMu.Unlock()

			// Commands, active setup wizard sessions, stale keyboard buttons, and stickers/voice
			// should be handled immediately without buffering.
			if upd.Message.IsCommand() || inSetup || isStaleKeyboardButton(upd.Message.Text) || upd.Message.Sticker != nil || upd.Message.Voice != nil {
				stateMu.Lock()
				err := handleMessages(ctx, logger, &cfg, tg, sessions, []*tgbotapi.Message{upd.Message})
				stateMu.Unlock()
				if err != nil {
					logger.Printf("handleMessages error: %v", err)
				}
				continue
			}

			mgCollector.Add(upd.Message)
		}
	}
}

func handleMessage(ctx context.Context, logger *log.Logger, cfg *Config, tg *telegram.Client, sessions map[int64]*setup.Wizard, msg *tgbotapi.Message) error {
	return handleMessages(ctx, logger, cfg, tg, sessions, []*tgbotapi.Message{msg})
}

func handleMessages(ctx context.Context, logger *log.Logger, cfg *Config, tg *telegram.Client, sessions map[int64]*setup.Wizard, msgs []*tgbotapi.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	msg := msgs[0]
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
			_, _ = tg.SendHTML(chatID, "\U0001f44b <b>PostXLinkedInBot is ready!</b>\n\n\U0001f4f8 Send a <b>photo, document, or video with caption</b> to post to your connected platforms.\n\U0001f4dd Or send a <b>text message</b> to post text-only.\n\nUse /help to see all commands.")
			return nil
		case "help":
			help := "\U0001f4d6 <b>PostXLinkedInBot \u2014 Commands</b>\n\n" +
				"\U0001f4f8 <b>Post content:</b>\n" +
				"\u2022 Send a <b>photo, document (PDF/Word/PPT), or video with caption</b> \u2192 media post\n" +
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

	// Reject stickers and voice messages.
	if msg.Sticker != nil || msg.Voice != nil {
		_, _ = tg.SendHTML(chatID, "\u274c Unsupported media type.\n\n\U0001f4f8 Send a <b>photo, document (PDF/Word/PPT), or video with caption</b>.\n\U0001f4dd Or send a <b>text message</b> for a text-only post.")
		return nil
	}

	// Extract inbound media from all messages in the batch
	type mediaItem struct {
		inbound *InboundMedia
		msg     *tgbotapi.Message
	}
	var mediaItems []mediaItem
	var filenames []string
	for _, m := range msgs {
		if inb := ExtractInboundMedia(m); inb != nil {
			mediaItems = append(mediaItems, mediaItem{inbound: inb, msg: m})
			if inb.Filename != "" {
				filenames = append(filenames, inb.Filename)
			}
		}
	}

	// Extract text, caption, and entities across all messages in the batch.
	// Note: when a user forwards a message with a comment in Telegram, the batch contains
	// both the user's comment message (m.Text) and the forwarded media message (m.Caption).
	var captionText string
	var captionEntities []tgbotapi.MessageEntity
	var captionFirstURL string
	var commentText string
	var commentEntities []tgbotapi.MessageEntity
	var commentFirstURL string
	var forwardChannelUsername string
	var forwardMessageID int
	isForwarded := false

	for _, m := range msgs {
		if isMessageForwarded(m) {
			isForwarded = true
		}
		if forwardChannelUsername == "" && m.ForwardFromChat != nil && m.ForwardFromChat.UserName != "" && m.ForwardFromMessageID > 0 {
			forwardChannelUsername = m.ForwardFromChat.UserName
			forwardMessageID = m.ForwardFromMessageID
		}
		if captionText == "" && m.Caption != "" {
			captionEntities = m.CaptionEntities
			if len(captionEntities) == 0 {
				captionEntities = m.Entities
			}
			captionFirstURL = ExtractFirstURL(m.Caption, captionEntities)
			captionText = ExpandTextLinks(m.Caption, captionEntities)
		}
		if commentText == "" && m.Text != "" {
			commentEntities = m.Entities
			if len(commentEntities) == 0 {
				commentEntities = m.CaptionEntities
			}
			commentFirstURL = ExtractFirstURL(m.Text, commentEntities)
			commentText = ExpandTextLinks(m.Text, commentEntities)
		}
	}

	var postText string
	var rawFirstURL string
	switch {
	case captionText != "" && commentText == "":
		postText = captionText
		rawFirstURL = captionFirstURL
	case captionText == "" && commentText != "":
		postText = commentText
		rawFirstURL = commentFirstURL
	case captionText != "" && commentText != "":
		if isRedundantFilenameComment(commentText, captionText, filenames) {
			postText = captionText
			rawFirstURL = captionFirstURL
		} else {
			postText = strings.TrimSpace(commentText) + "\n\n" + strings.TrimSpace(captionText)
			if commentFirstURL != "" {
				rawFirstURL = commentFirstURL
			} else {
				rawFirstURL = captionFirstURL
			}
		}
	}

	// If media (such as a forwarded PDF/document) has no caption or comment, fall back to its filename
	// so documents are posted directly rather than rejected.
	if len(mediaItems) > 0 && strings.TrimSpace(postText) == "" {
		for _, item := range mediaItems {
			if item.inbound.Category == MediaCategoryDocument || hasMeaningfulFilename(item.inbound.Filename) {
				postText = item.inbound.Filename
				break
			}
		}
	}

	for _, item := range mediaItems {
		if item.inbound.Category == MediaCategoryAudio {
			logger.Printf("[%d] rejected audio media: %s", chatID, item.inbound.Filename)
			_, _ = tg.SendHTML(chatID, "\u26a0\ufe0f LinkedIn API does not support direct audio feed posts (audio is supported only on profile work samples).\n\nTo share audio on LinkedIn, please convert it to a video (e.g. MP4) and send it with a caption.")
			return nil
		}
		if item.inbound.Category == MediaCategoryUnknown {
			logger.Printf("[%d] rejected unsupported file type: %s (%s)", chatID, item.inbound.Filename, item.inbound.MIME)
			_, _ = tg.SendHTML(chatID, fmt.Sprintf("\u274c Unsupported file type: <code>%s</code>\n\n<b>Supported on LinkedIn:</b>\n\u2022 <b>Images:</b> JPG, PNG, GIF, WebP, HEIC\n\u2022 <b>Documents:</b> PDF, DOC/DOCX, PPT/PPTX, ODT, ODS\n\u2022 <b>Videos:</b> MP4, MOV, AVI, WEBM, MKV", item.inbound.Filename))
			return nil
		}
	}

	if len(mediaItems) > 0 && strings.TrimSpace(postText) == "" {
		logger.Printf("[%d] rejected media with empty caption (forwarded=%v, items=%d)", chatID, isForwarded, len(mediaItems))
		if isForwarded {
			_, _ = tg.SendHTML(chatID, "\u26a0\ufe0f <b>Forwarded media has no caption.</b>\n\nA caption is required for your post text.\n\n\U0001f449 <i>Tip: When forwarding in Telegram, tap the preview to add a comment before sending, or send the file directly with a caption.</i>")
		} else {
			_, _ = tg.SendText(chatID, "Missing caption. Add a caption to your media (this becomes the post text).")
		}
		return nil
	}
	if len(mediaItems) == 0 && strings.TrimSpace(postText) == "" {
		_, _ = tg.SendHTML(chatID, "\U0001f4f8 Send or forward a <b>photo, document, or video with caption</b> for a media post.\n\U0001f4dd Or send a <b>text message</b> for a text-only post.")
		return nil
	}

	var progressMsgIDs []int
	sendProgress := func(text string) {
		if m, err := tg.SendText(chatID, text); err == nil {
			progressMsgIDs = append(progressMsgIDs, m.MessageID)
		}
	}
	deleteProgress := func() {
		for _, id := range progressMsgIDs {
			if err := tg.DeleteMessage(chatID, id); err != nil {
				logger.Printf("[%d] deleteMessage %d: %v", chatID, id, err)
			}
		}
		progressMsgIDs = nil
	}
	defer deleteProgress()

	var downloadedFiles []telegram.DownloadedFile
	if len(mediaItems) > 0 {
		if len(mediaItems) == 1 {
			sendProgress(fmt.Sprintf("\u23f3 Downloading %s...", mediaItems[0].inbound.Category))
		} else {
			sendProgress(fmt.Sprintf("\u23f3 Downloading %d media items...", len(mediaItems)))
		}

		for idx, item := range mediaItems {
			maxBytes := cfg.MaxImageBytes
			if item.inbound.Category == MediaCategoryDocument || item.inbound.Category == MediaCategoryVideo {
				if maxBytes < 100<<20 {
					maxBytes = 100 << 20
				}
			}

			if item.inbound.FileSize > maxBytes {
				deleteProgress()
				logger.Printf("[%d] rejected file too large before download: %s (%d bytes > %d max)", chatID, item.inbound.Filename, item.inbound.FileSize, maxBytes)
				_, _ = tg.SendHTML(chatID, fmt.Sprintf("\u274c File too large (%d bytes). Max allowed is %d bytes.\n\nTry compressing or resizing.", item.inbound.FileSize, maxBytes))
				return errors.New("file too large")
			}

			logger.Printf("[%d] downloading %s (%d/%d): file_id=%s filename=%s size=%d",
				chatID, item.inbound.Category, idx+1, len(mediaItems), item.inbound.FileID, item.inbound.Filename, item.inbound.FileSize)

			d, err := tg.DownloadMedia(ctx, item.inbound.FileID, item.inbound.Filename, item.inbound.MIME)
			if err != nil {
				deleteProgress()
				logger.Printf("[%d] download item %d failed: %v", chatID, idx+1, err)
				_, _ = tg.SendHTML(chatID, "\u274c Failed to download file from Telegram. Try sending it again.")
				return err
			}

			if int64(len(d.Bytes)) > maxBytes {
				deleteProgress()
				logger.Printf("[%d] rejected file too large: %s (%d bytes > %d max)", chatID, item.inbound.Filename, len(d.Bytes), maxBytes)
				_, _ = tg.SendHTML(chatID, fmt.Sprintf("\u274c File too large (%d bytes). Max allowed is %d bytes.\n\nTry compressing or resizing.", len(d.Bytes), maxBytes))
				return errors.New("file too large")
			}
			downloadedFiles = append(downloadedFiles, d)
		}
		logger.Printf("[%d] downloaded %d media items successfully", chatID, len(downloadedFiles))
	} else {
		if cfg.Debug {
			logger.Printf("text post received: chat=%d text_len=%d", chatID, len(postText))
		}
	}

	allImages := len(mediaItems) > 0
	for _, item := range mediaItems {
		if item.inbound.Category != MediaCategoryImage {
			allImages = false
			break
		}
	}

	caption := postText
	// Sanitize newlines: remove carriage returns to avoid truncation issues on some platforms.
	caption = strings.ReplaceAll(caption, "\r", "")

	// Mode: direct API calls to X + LinkedIn.
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	var postURLs []string
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

	linkURL := ""
	var linkMeta *opengraph.Metadata
	if len(downloadedFiles) == 0 {
		linkURL = rawFirstURL
		if linkURL == "" {
			linkURL = ExtractFirstURL(caption, nil)
		}
		if linkURL != "" {
			fetchCtx, fetchCancel := context.WithTimeout(ctx, 12*time.Second)
			if m, err := opengraph.Fetch(fetchCtx, httpClient, linkURL); err == nil {
				linkMeta = m
			} else {
				logger.Printf("[%d] OpenGraph fetch failed for %s: %v", chatID, linkURL, err)
			}
			if (linkMeta == nil || linkMeta.ImageURL == "") && forwardChannelUsername != "" && forwardMessageID > 0 {
				if tgImg := opengraph.FetchTelegramWebPreviewImage(fetchCtx, httpClient, forwardChannelUsername, forwardMessageID); tgImg != "" {
					if linkMeta == nil {
						linkMeta = &opengraph.Metadata{}
					}
					linkMeta.ImageURL = tgImg
				}
			}
			fetchCancel()
		}
	}

	if cfg.EnableX && cfg.XUserBearerToken != "" {
		sendProgress("\u23f3 Posting to X...")

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

		if len(downloadedFiles) > 0 && allImages {
			var mediaIDs []string
			limit := len(downloadedFiles)
			if limit > 4 {
				limit = 4
			}
			for i := 0; i < limit; i++ {
				d := downloadedFiles[i]
				mediaID, err := xClient.UploadMedia(ctx, d.Base64, d.MIME)
				if err != nil {
					errs = append(errs, "X upload: "+err.Error())
					if isXAuthError(err) {
						errs = append(errs, xAuthHint)
					}
					break
				}
				mediaIDs = append(mediaIDs, mediaID)
			}
			if len(mediaIDs) > 0 {
				if tweetID, err := xClient.CreatePost(ctx, xText, mediaIDs); err != nil {
					errs = append(errs, "X post: "+err.Error())
					if isXAuthError(err) {
						errs = append(errs, xAuthHint)
					}
				} else {
					if u := FormatXPostURL(tweetID); u != "" {
						postURLs = append(postURLs, u)
					}
				}
			}
		} else if len(downloadedFiles) == 1 && mediaItems[0].inbound.Category == MediaCategoryDocument {
			// X doesn't support documents as media attachments; post caption as text post.
			if tweetID, err := xClient.CreatePost(ctx, xText, nil); err != nil {
				errs = append(errs, "X post: "+err.Error())
				if isXAuthError(err) {
					errs = append(errs, xAuthHint)
				}
			} else {
				if u := FormatXPostURL(tweetID); u != "" {
					postURLs = append(postURLs, u)
				}
			}
		} else if len(downloadedFiles) == 1 && mediaItems[0].inbound.Category == MediaCategoryVideo {
			// Attempt media upload or fallback to text.
			dl := downloadedFiles[0]
			mediaID, err := xClient.UploadMedia(ctx, dl.Base64, dl.MIME)
			if err == nil {
				if tweetID, err := xClient.CreatePost(ctx, xText, []string{mediaID}); err == nil {
					if u := FormatXPostURL(tweetID); u != "" {
						postURLs = append(postURLs, u)
					}
				} else {
					if tweetID, terr := xClient.CreatePost(ctx, xText, nil); terr == nil {
						if u := FormatXPostURL(tweetID); u != "" {
							postURLs = append(postURLs, u)
						}
					} else {
						errs = append(errs, "X post: "+err.Error())
					}
				}
			} else {
				if tweetID, terr := xClient.CreatePost(ctx, xText, nil); terr == nil {
					if u := FormatXPostURL(tweetID); u != "" {
						postURLs = append(postURLs, u)
					}
				} else {
					errs = append(errs, "X upload: "+err.Error())
				}
			}
		} else {
			// Text-only or link-preview post.
			if tweetID, err := xClient.CreatePost(ctx, xText, nil); err != nil {
				errs = append(errs, "X post: "+err.Error())
				if isXAuthError(err) {
					errs = append(errs, xAuthHint)
				}
			} else {
				if u := FormatXPostURL(tweetID); u != "" {
					postURLs = append(postURLs, u)
				}
			}
		}
	}

	if cfg.EnableLinkedIn && cfg.LinkedInAccessToken != "" && cfg.LinkedInAuthorURN != "" {
		sendProgress("\u23f3 Posting to LinkedIn...")

		liClient := linkedin.New(httpClient, cfg.LinkedInAccessToken, cfg.LinkedInVersion)

		if len(downloadedFiles) > 1 && allImages {
			// Multi-image post!
			logger.Printf("[%d] uploading %d images to LinkedIn...", chatID, len(downloadedFiles))
			var imageURNs []string
			var uploadErr error
			for idx, d := range downloadedFiles {
				uploadURL, imgURN, err := liClient.InitializeImageUpload(ctx, cfg.LinkedInAuthorURN)
				if err != nil {
					uploadErr = fmt.Errorf("LinkedIn image %d init: %w", idx+1, err)
					break
				}
				if err := liClient.UploadImageBytes(ctx, uploadURL, d.MIME, d.Bytes); err != nil {
					uploadErr = fmt.Errorf("LinkedIn image %d upload: %w", idx+1, err)
					break
				}
				imageURNs = append(imageURNs, imgURN)
			}

			if uploadErr != nil {
				errs = append(errs, uploadErr.Error())
				logger.Printf("[%d] LinkedIn image upload failed: %v", chatID, uploadErr)
			} else {
				logger.Printf("[%d] creating LinkedIn multiImage post with %d images...", chatID, len(imageURNs))
				if postID, err := liClient.CreateMultiImagePost(ctx, cfg.LinkedInAuthorURN, caption, imageURNs); err != nil {
					errs = append(errs, "LinkedIn multi-image post failed: "+err.Error())
					logger.Printf("[%d] LinkedIn multiImage post failed: %v", chatID, err)
				} else {
					logger.Printf("[%d] LinkedIn multiImage post created: %s", chatID, postID)
					if u := FormatLinkedInPostURL(postID); u != "" {
						postURLs = append(postURLs, u)
					}
				}
			}
		} else if len(downloadedFiles) >= 1 {
			for idx, dl := range downloadedFiles {
				inbound := mediaItems[idx].inbound
				switch inbound.Category {
				case MediaCategoryImage:
					logger.Printf("[%d] uploading image to LinkedIn...", chatID)
					uploadURL, imageURN, err := liClient.InitializeImageUpload(ctx, cfg.LinkedInAuthorURN)
					if err != nil {
						errs = append(errs, "LinkedIn init: "+err.Error())
						logger.Printf("[%d] LinkedIn init failed: %v", chatID, err)
					} else if err := liClient.UploadImageBytes(ctx, uploadURL, dl.MIME, dl.Bytes); err != nil {
						errs = append(errs, "LinkedIn upload failed: "+err.Error())
						logger.Printf("[%d] LinkedIn upload failed: %v", chatID, err)
					} else if postID, err := liClient.CreateImagePost(ctx, cfg.LinkedInAuthorURN, caption, imageURN, dl.Filename); err != nil {
						errs = append(errs, "LinkedIn post failed: "+err.Error())
						logger.Printf("[%d] LinkedIn post failed: %v", chatID, err)
					} else {
						logger.Printf("[%d] LinkedIn image post created: %s", chatID, postID)
						if u := FormatLinkedInPostURL(postID); u != "" {
							postURLs = append(postURLs, u)
						}
					}

				case MediaCategoryDocument:
					logger.Printf("[%d] uploading document to LinkedIn (%s)...", chatID, dl.Filename)
					uploadURL, docURN, err := liClient.InitializeDocumentUpload(ctx, cfg.LinkedInAuthorURN)
					if err != nil {
						errs = append(errs, "LinkedIn doc init: "+err.Error())
						logger.Printf("[%d] LinkedIn doc init failed: %v", chatID, err)
					} else if err := liClient.UploadDocumentBytes(ctx, uploadURL, dl.MIME, dl.Bytes); err != nil {
						errs = append(errs, "LinkedIn doc upload: "+err.Error())
						logger.Printf("[%d] LinkedIn doc upload failed: %v", chatID, err)
					} else if postID, err := liClient.CreateDocumentPost(ctx, cfg.LinkedInAuthorURN, caption, docURN, dl.Filename); err != nil {
						errs = append(errs, "LinkedIn doc post: "+err.Error())
						logger.Printf("[%d] LinkedIn doc post failed: %v", chatID, err)
					} else {
						logger.Printf("[%d] LinkedIn doc post created: %s", chatID, postID)
						if u := FormatLinkedInPostURL(postID); u != "" {
							postURLs = append(postURLs, u)
						}
					}

				case MediaCategoryVideo:
					logger.Printf("[%d] uploading video to LinkedIn...", chatID)
					videoURN, uploadToken, instructions, err := liClient.InitializeVideoUpload(ctx, cfg.LinkedInAuthorURN, int64(len(dl.Bytes)))
					if err != nil {
						errs = append(errs, "LinkedIn video init: "+err.Error())
						logger.Printf("[%d] LinkedIn video init failed: %v", chatID, err)
					} else if etags, err := liClient.UploadVideoParts(ctx, instructions, dl.Bytes); err != nil {
						errs = append(errs, "LinkedIn video upload: "+err.Error())
						logger.Printf("[%d] LinkedIn video upload failed: %v", chatID, err)
					} else if err := liClient.FinalizeVideoUpload(ctx, videoURN, uploadToken, etags); err != nil {
						errs = append(errs, "LinkedIn video finalize: "+err.Error())
						logger.Printf("[%d] LinkedIn video finalize failed: %v", chatID, err)
					} else if postID, err := liClient.CreateVideoPost(ctx, cfg.LinkedInAuthorURN, caption, videoURN, dl.Filename); err != nil {
						errs = append(errs, "LinkedIn video post: "+err.Error())
						logger.Printf("[%d] LinkedIn video post failed: %v", chatID, err)
					} else {
						logger.Printf("[%d] LinkedIn video post created: %s", chatID, postID)
						if u := FormatLinkedInPostURL(postID); u != "" {
							postURLs = append(postURLs, u)
						}
					}

				default:
					errs = append(errs, "LinkedIn: unsupported media category")
				}
			}
		} else {
			// Text or link post without inbound Telegram media.
			// If the post contains URLs, fetch the first URL's preview image and attach/upload it
			// directly as a LinkedIn image post (just like Telegram displays the preview image).
			if linkURL != "" {
				articleTitle := ""
				articleDesc := ""
				previewImgURN := ""

				if linkMeta != nil {
					articleTitle = linkMeta.Title
					articleDesc = linkMeta.Description

					if linkMeta.ImageURL != "" {
						sendProgress("\u23f3 Fetching link preview image...")
						imgCtx, imgCancel := context.WithTimeout(ctx, 15*time.Second)
						imgBytes, mimeType, err := opengraph.FetchImage(imgCtx, httpClient, linkMeta.ImageURL, 10<<20)
						if err != nil && forwardChannelUsername != "" && forwardMessageID > 0 {
							if tgImg := opengraph.FetchTelegramWebPreviewImage(imgCtx, httpClient, forwardChannelUsername, forwardMessageID); tgImg != "" && tgImg != linkMeta.ImageURL {
								imgBytes, mimeType, err = opengraph.FetchImage(imgCtx, httpClient, tgImg, 10<<20)
							}
						}
						imgCancel()
						if err != nil {
							logger.Printf("[%d] failed to download preview image from %s: %v", chatID, linkMeta.ImageURL, err)
						} else {
							uploadURL, imgURN, err := liClient.InitializeImageUpload(ctx, cfg.LinkedInAuthorURN)
							if err != nil {
								logger.Printf("[%d] failed to init LinkedIn preview image upload: %v", chatID, err)
							} else if err := liClient.UploadImageBytes(ctx, uploadURL, mimeType, imgBytes); err != nil {
								logger.Printf("[%d] failed to upload LinkedIn preview image: %v", chatID, err)
							} else {
								previewImgURN = imgURN
							}
						}
					}
				}

				if previewImgURN != "" {
					imgTitle := articleTitle
					if imgTitle == "" {
						imgTitle = "preview.jpg"
					}
					logger.Printf("[%d] creating LinkedIn image post with attached preview image (link=%s, image=%s)...", chatID, linkURL, previewImgURN)
					if postID, err := liClient.CreateImagePost(ctx, cfg.LinkedInAuthorURN, caption, previewImgURN, imgTitle); err == nil {
						logger.Printf("[%d] LinkedIn preview image post created: %s", chatID, postID)
						if u := FormatLinkedInPostURL(postID); u != "" {
							postURLs = append(postURLs, u)
						}
					} else {
						logger.Printf("[%d] LinkedIn preview image post failed (%v), falling back to article/text post", chatID, err)
						previewImgURN = ""
					}
				}

				if previewImgURN == "" {
					logger.Printf("[%d] creating LinkedIn article post (link=%s)...", chatID, linkURL)
					if postID, err := liClient.CreateArticlePost(ctx, cfg.LinkedInAuthorURN, caption, linkURL, articleTitle, articleDesc, ""); err == nil {
						logger.Printf("[%d] LinkedIn article post created: %s", chatID, postID)
						if u := FormatLinkedInPostURL(postID); u != "" {
							postURLs = append(postURLs, u)
						}
					} else {
						logger.Printf("[%d] LinkedIn article post failed (%v), falling back to text post", chatID, err)
						if postID, terr := liClient.CreateTextPost(ctx, cfg.LinkedInAuthorURN, caption); terr != nil {
							errs = append(errs, fmt.Sprintf("LinkedIn post failed (article: %v; fallback: %v)", err, terr))
							logger.Printf("[%d] LinkedIn fallback text post failed: %v", chatID, terr)
						} else {
							logger.Printf("[%d] LinkedIn text fallback created: %s", chatID, postID)
							if u := FormatLinkedInPostURL(postID); u != "" {
								postURLs = append(postURLs, u)
							}
						}
					}
				}
			} else {
				logger.Printf("[%d] creating LinkedIn text post...", chatID)
				if postID, err := liClient.CreateTextPost(ctx, cfg.LinkedInAuthorURN, caption); err != nil {
					errs = append(errs, "LinkedIn post: "+err.Error())
					logger.Printf("[%d] LinkedIn text post failed: %v", chatID, err)
				} else {
					logger.Printf("[%d] LinkedIn text post created: %s", chatID, postID)
					if u := FormatLinkedInPostURL(postID); u != "" {
						postURLs = append(postURLs, u)
					}
				}
			}
		}
	}

	deleteProgress()

	// Build summary message.
	var summary strings.Builder
	if len(postURLs) > 0 && len(errs) == 0 {
		summary.WriteString("\u2705 <b>Posted successfully!</b>\n\n")
		for _, u := range postURLs {
			summary.WriteString(u + "\n")
		}
	} else if len(postURLs) == 0 && len(errs) == 0 {
		summary.WriteString("\u2705 <b>Posted successfully!</b>\n")
	} else if len(postURLs) > 0 && len(errs) > 0 {
		summary.WriteString("\u26a0\ufe0f <b>Partially posted:</b>\n\n")
		for _, u := range postURLs {
			summary.WriteString(u + "\n")
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
	if _, err := tg.SendHTML(chatID, summary.String()); err != nil {
		logger.Printf("[%d] send summary failed: %v", chatID, err)
	} else {
		logger.Printf("[%d] summary sent successfully", chatID)
	}

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

	// Direct mode.
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
	var b strings.Builder
	b.WriteString("\U0001f4ca <b>Bot Status</b>\n\n")
	if cfg.AllowedChatID != 0 {
		b.WriteString(fmt.Sprintf("\u2022 <b>Locked chat:</b> %d\n", cfg.AllowedChatID))
	} else {
		b.WriteString("\u2022 <b>Locked chat:</b> no\n")
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

func hasMeaningfulFilename(fn string) bool {
	fn = strings.TrimSpace(strings.ToLower(fn))
	switch fn {
	case "", "photo.jpg", "video.mp4", "animation.gif", "videonote.mp4", "media", "audio", "document":
		return false
	default:
		return true
	}
}

func normalizeAlphaNum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isRedundantFilenameComment(comment, caption string, filenames []string) bool {
	nc := normalizeAlphaNum(comment)
	if nc == "" {
		return true
	}
	if ncap := normalizeAlphaNum(caption); ncap != "" && strings.Contains(ncap, nc) {
		return true
	}
	for _, fn := range filenames {
		nfn := normalizeAlphaNum(fn)
		if nfn != "" && (strings.Contains(nfn, nc) || strings.Contains(nc, nfn)) {
			return true
		}
	}
	return false
}
