package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Mode string

const (
	ModeDirect Mode = "direct"
)

type Config struct {
	Mode Mode `json:"mode"`

	AllowedChatID int64 `json:"allowed_chat_id,omitempty"`

	EnableX        bool `json:"enable_x"`
	EnableLinkedIn bool `json:"enable_linkedin"`

	// Optional "AI agent" webhook to transform caption (hashtags, formatting, etc).
	// This lets you plug in LangChain/anything without hardcoding a vendor.
	AgentWebhookURL    string `json:"agent_webhook_url,omitempty"`
	AgentSharedSecret  string `json:"agent_shared_secret,omitempty"`
	AgentSecretEnabled bool   `json:"agent_secret_enabled,omitempty"`

	// direct mode
	XUserBearerToken string `json:"x_user_bearer_token,omitempty"`
	XRefreshToken    string `json:"x_refresh_token,omitempty"`
	XClientID        string `json:"x_client_id,omitempty"`
	XClientSecret    string `json:"x_client_secret,omitempty"`
	XAPIBaseURL      string `json:"x_api_base_url,omitempty"`

	LinkedInAccessToken string `json:"linkedin_access_token,omitempty"`
	LinkedInAuthorURN   string `json:"linkedin_author_urn,omitempty"`
	LinkedInVersion     string `json:"linkedin_version,omitempty"`

	MaxImageBytes int64 `json:"max_image_bytes,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

func Load(path string) (Config, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func Save(path string, cfg Config) error {
	cfg.UpdatedAt = time.Now().UTC()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp := path + ".tmp"
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
