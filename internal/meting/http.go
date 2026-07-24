package meting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var defaultClient = &http.Client{Timeout: 20 * time.Second}

func doRequest(ctx context.Context, method, rawURL string, headers map[string]string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, 0, err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	resp, err := defaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return data, resp.StatusCode, fmt.Errorf("upstream %s %s: %s", method, rawURL, resp.Status)
	}
	return data, resp.StatusCode, nil
}

func httpGet(ctx context.Context, rawURL string, query map[string]string, headers map[string]string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	values := u.Query()
	for key, value := range query {
		values.Set(key, value)
	}
	u.RawQuery = values.Encode()
	data, _, err := doRequest(ctx, http.MethodGet, u.String(), headers, nil)
	return data, err
}

func httpPostForm(ctx context.Context, rawURL string, form url.Values, headers map[string]string) ([]byte, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	data, _, err := doRequest(ctx, http.MethodPost, rawURL, headers, strings.NewReader(form.Encode()))
	return data, err
}

func httpPostJSON(ctx context.Context, rawURL string, payload any, headers map[string]string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return httpPostRaw(ctx, rawURL, body, headers)
}

func httpPostRaw(ctx context.Context, rawURL string, body []byte, headers map[string]string) ([]byte, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	data, _, err := doRequest(ctx, http.MethodPost, rawURL, headers, bytes.NewReader(body))
	return data, err
}

func stripJSONP(input string) string {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "(") || !strings.HasSuffix(input, ")") {
		return input
	}
	start := strings.IndexByte(input, '(')
	end := strings.LastIndexByte(input, ')')
	if start < 0 || end <= start {
		return input
	}
	return input[start+1 : end]
}

func parseCookieString(cookie string) map[string]string {
	result := map[string]string{}
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return result
}
