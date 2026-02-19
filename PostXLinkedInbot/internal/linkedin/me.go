package linkedin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetMyPersonURN fetches the authenticated member id and returns urn:li:person:<id>.
// Uses the /v2/userinfo endpoint (OIDC standard) since /v2/me is deprecated.
// This is used only during setup to reduce user friction.
func (c *Client) GetMyPersonURN(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.linkedin.com/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(&io.LimitedReader{R: res.Body, N: 8 << 10})
		return "", fmt.Errorf("linkedin userinfo failed: %s: %s", res.Status, string(body))
	}

	var payload struct {
		Sub  string `json:"sub"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Sub == "" {
		return "", fmt.Errorf("linkedin userinfo missing sub")
	}
	return "urn:li:person:" + payload.Sub, nil
}
