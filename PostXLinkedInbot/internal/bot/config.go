package bot

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN,notEmpty"`
	AllowedChatID    int64  `env:"ALLOWED_CHAT_ID" envDefault:"0"`

	// Where the bot persists setup done via Telegram chat (JSON).
	ConfigPath string `env:"CONFIG_PATH" envDefault:"data/config.json"`

	// Optional "AI agent" webhook (caption transformer).
	AgentWebhookURL   string `env:"AGENT_WEBHOOK_URL" envDefault:""`
	AgentSharedSecret string `env:"AGENT_SHARED_SECRET" envDefault:""`

	// Optional: n8n webhook that will do the posting (if set, bot uses n8n mode).
	N8NWebhookURL string `env:"N8N_WEBHOOK_URL" envDefault:""`

	// Shared secret between bot and n8n workflow. If set here, n8n must verify it.
	N8NSharedSecret string `env:"N8N_SHARED_SECRET" envDefault:""`

	EnableX        bool `env:"ENABLE_X" envDefault:"true"`
	EnableLinkedIn bool `env:"ENABLE_LINKEDIN" envDefault:"true"`

	XUserBearerToken string `env:"X_USER_BEARER_TOKEN" envDefault:""`
	XAPIBaseURL      string `env:"X_API_BASE_URL" envDefault:"https://api.x.com"`

	LinkedInAccessToken string `env:"LINKEDIN_ACCESS_TOKEN" envDefault:""`
	LinkedInAuthorURN   string `env:"LINKEDIN_AUTHOR_URN" envDefault:""`
	LinkedInVersion     string `env:"LINKEDIN_VERSION" envDefault:"202404"`

	// Hard limit (bytes) to avoid huge uploads; X image limits are commonly small.
	MaxImageBytes int64 `env:"MAX_IMAGE_BYTES" envDefault:"5000000"`

	// If true, bot will respond with more detail (still no secrets).
	Debug bool `env:"DEBUG" envDefault:"false"`
}

func LoadConfig() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.MaxImageBytes <= 0 {
		return Config{}, errors.New("MAX_IMAGE_BYTES must be > 0")
	}
	cfg.ConfigPath = filepath.Clean(cfg.ConfigPath)
	return cfg, nil
}

type Runtime struct {
	PollTimeout time.Duration
}
