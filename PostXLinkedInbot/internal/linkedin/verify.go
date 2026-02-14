package linkedin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type UserInfo struct {
	Sub   string `json:"sub,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

func (c *Client) VerifyUserInfo(ctx context.Context) (UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.linkedin.com/v2/userinfo", nil)
	if err != nil {
		return UserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return UserInfo{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(&io.LimitedReader{R: res.Body, N: 8 << 10})
		return UserInfo{}, fmt.Errorf("linkedin userinfo failed: %s: %s", res.Status, string(body))
	}
	var ui UserInfo
	if err := json.NewDecoder(res.Body).Decode(&ui); err != nil {
		return UserInfo{}, err
	}
	return ui, nil
}
