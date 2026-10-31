package discordsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const DefaultBase = "https://discord.com/api/v10"

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// Do sends one request and decodes the JSON answer into out (may be nil).
// A 429 is retried after the delay Discord asks for.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+c.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "StokeholdSetup (https://github.com/stokehold-gsm/stokehold, 0.1)")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 5 {
			wait, _ := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64)
			select {
			case <-time.After(time.Duration(wait*1000)*time.Millisecond + 100*time.Millisecond):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(data))
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}
