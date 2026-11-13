package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type cliError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
}

func (e *cliError) Error() string        { return e.Code + ": " + e.Message }
func failure(code, message string) error { return &cliError{Code: code, Message: message} }
func newClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (a *app) do(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	if a.connection.APIKey == "" {
		return nil, failure("key_missing", "set the configured API key environment variable or api_key in config")
	}
	req, err := http.NewRequestWithContext(ctx, method, a.connection.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, failure("request_invalid", "cannot create request")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	switch a.connection.Protocol {
	case "anthropic":
		req.Header.Set("x-api-key", a.connection.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		req.Header.Set("x-goog-api-key", a.connection.APIKey)
	default:
		req.Header.Set("Authorization", "Bearer "+a.connection.APIKey)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("request_cancelled", "request cancelled or timed out")
		}
		return nil, failure("connection_failed", "cannot reach the configured API")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &cliError{Code: "api_error", Message: http.StatusText(resp.StatusCode), Status: resp.StatusCode}
	}
	return resp, nil
}
func (a *app) jsonRequest(ctx context.Context, method, path, contentType string, body []byte) (json.RawMessage, error) {
	resp, err := a.do(ctx, method, path, contentType, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil || len(data) > 32<<20 {
		return nil, failure("response_too_large", "API response exceeds 32 MiB or could not be read")
	}
	if !json.Valid(data) {
		return nil, failure("response_invalid", "API did not return valid JSON")
	}
	return json.RawMessage(data), nil
}
func writeNew(path string, data []byte) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return failure("output_write", "cannot create output directory")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failure("output_write", "cannot create file; it may already exist")
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return failure("output_write", "cannot write file")
	}
	if err = f.Close(); err != nil {
		return failure("output_write", "cannot finish file")
	}
	success = true
	return nil
}
func saveStream(path string, r io.Reader) error {
	// Stage the full download before publishing a file, and never overwrite existing output.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return failure("output_write", "cannot create output directory")
	}
	f, err := os.CreateTemp(dir, ".motok-*")
	if err != nil {
		return failure("output_write", "cannot create temporary output")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = io.Copy(f, r); err != nil {
		return failure("download_failed", "media download interrupted")
	}
	if err = f.Close(); err != nil {
		return failure("output_write", "cannot finish output")
	}
	if err = os.Link(f.Name(), path); err != nil {
		return failure("output_write", "cannot publish output; it may already exist")
	}
	return nil
}
func (a *app) saveImage(ctx context.Context, data json.RawMessage, path string) error {
	var response struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						Data string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &response) != nil {
		return failure("response_invalid", "invalid image response")
	}
	if len(response.Data) > 0 {
		if response.Data[0].B64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(response.Data[0].B64)
			if err != nil {
				return failure("image_invalid", "invalid base64 image")
			}
			return writeNew(path, decoded)
		}
		if response.Data[0].URL != "" {
			return a.downloadURL(ctx, response.Data[0].URL, path)
		}
	}
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				decoded, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					return failure("image_invalid", "invalid base64 image")
				}
				return writeNew(path, decoded)
			}
		}
	}
	return failure("image_missing", "response contains no downloadable image")
}
func (a *app) downloadURL(ctx context.Context, raw, path string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && localHost(u.Hostname()))) {
		return failure("download_url_invalid", "media URL must use HTTPS (HTTP loopback is allowed)")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return failure("download_url_invalid", "invalid media URL")
	}
	// Media URLs may belong to storage providers. Never attach an API credential.
	resp, err := a.client.Do(req)
	if err != nil {
		return failure("download_failed", "cannot download generated media")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return failure("download_failed", "media server rejected download")
	}
	return saveStream(path, resp.Body)
}
func escapeModel(model string) (string, error) {
	model = strings.TrimPrefix(model, "models/")
	if model == "" || strings.ContainsAny(model, "/?#\\\x00\r\n") || model == "." || model == ".." {
		return "", failure("model_invalid", "invalid model identifier")
	}
	return url.PathEscape(model), nil
}
