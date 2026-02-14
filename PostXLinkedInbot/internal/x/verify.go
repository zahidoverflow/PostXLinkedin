package x

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type MeResponse struct {
	Data struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"data"`
}

func (c *Client) Verify(ctx context.Context) (MeResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/2/users/me", nil)
	if err != nil {
		return MeResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return MeResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(&io.LimitedReader{R: res.Body, N: 8 << 10})
		return MeResponse{}, fmt.Errorf("x verify failed: %s: %s", res.Status, string(body))
	}
	var mr MeResponse
	if err := json.NewDecoder(res.Body).Decode(&mr); err != nil {
		return MeResponse{}, err
	}
	return mr, nil
}
