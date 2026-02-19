package x

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	AuthURL     = "https://x.com/i/oauth2/authorize"
	TokenURL    = "https://api.x.com/2/oauth2/token"
	RedirectURI = "https://127.0.0.1/callback"
	Scopes      = "tweet.read tweet.write users.read media.write offline.access"
)

// OAuthConfig holds the X app credentials used for the OAuth2 PKCE flow.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
}

// PKCEChallenge holds the verifier and challenge for the PKCE flow.
type PKCEChallenge struct {
	Verifier  string
	Challenge string
	Method    string // "plain"
}

// GeneratePKCE creates a random PKCE verifier and S256 challenge.
// S256 is recommended by the X API docs and is more secure than plain.
func GeneratePKCE() (PKCEChallenge, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return PKCEChallenge{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	// S256: challenge = BASE64URL(SHA256(verifier))
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	return PKCEChallenge{
		Verifier:  verifier,
		Challenge: challenge,
		Method:    "S256",
	}, nil
}

// BuildAuthURL constructs the full authorization URL the user should open.
func BuildAuthURL(clientID string, pkce PKCEChallenge) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("redirect_uri", RedirectURI)
	v.Set("scope", Scopes)
	v.Set("state", "setup")
	v.Set("code_challenge", pkce.Challenge)
	v.Set("code_challenge_method", pkce.Method)
	return AuthURL + "?" + v.Encode()
}

// ExtractCodeFromCallback extracts the authorization code from a callback URL.
// The user pastes something like https://127.0.0.1/callback?state=setup&code=XXXX
func ExtractCodeFromCallback(callbackURL string) (string, error) {
	// If user just pasted the code directly (no URL), return it.
	if !strings.Contains(callbackURL, "://") && !strings.Contains(callbackURL, "?") {
		return strings.TrimSpace(callbackURL), nil
	}
	u, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	code := u.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no 'code' parameter found in URL")
	}
	return code, nil
}

// TokenResponse is the JSON response from the token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// ExchangeCode exchanges an authorization code for access + refresh tokens.
func ExchangeCode(ctx context.Context, httpClient *http.Client, cfg OAuthConfig, code string, pkce PKCEChallenge) (TokenResponse, error) {
	data := url.Values{}
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", RedirectURI)
	data.Set("code_verifier", pkce.Verifier)

	req, err := http.NewRequestWithContext(ctx, "POST", TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)

	res, err := httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(&io.LimitedReader{R: res.Body, N: 8 << 10})
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenResponse{}, fmt.Errorf("token response parse error: %w (%s)", err, string(body))
	}
	if tr.Error != "" {
		return tr, fmt.Errorf("token error: %s: %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return tr, fmt.Errorf("no access_token in response: %s", string(body))
	}
	return tr, nil
}

// RefreshAccessToken uses a refresh token to get a new access + refresh token.
func RefreshAccessToken(ctx context.Context, httpClient *http.Client, cfg OAuthConfig, refreshToken string) (TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)

	res, err := httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(&io.LimitedReader{R: res.Body, N: 8 << 10})
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenResponse{}, fmt.Errorf("refresh response parse error: %w (%s)", err, string(body))
	}
	if tr.Error != "" {
		return tr, fmt.Errorf("refresh error: %s: %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return tr, fmt.Errorf("no access_token in refresh response: %s", string(body))
	}
	return tr, nil
}
