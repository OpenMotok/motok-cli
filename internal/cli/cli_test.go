package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func envConfig(t *testing.T, c Config) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writeConfig(path, c); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"NMODEL_CONFIG": path}
}
func runTest(t *testing.T, env map[string]string, input string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(input), &out, &errOut, func(k string) string { return env[k] })
	return code, out.String(), errOut.String()
}
func TestOfficialProtocolsAndCustomPrefixes(t *testing.T) {
	tests := []struct{ provider, path, response, header string }{
		{"openai", "/custom/v1/chat/completions", `{"choices":[{"message":{"content":"hello"}}]}`, "Authorization"},
		{"anthropic", "/custom/v1/messages", `{"content":[{"type":"text","text":"hello"}]}`, "x-api-key"},
		{"gemini", "/custom/v1/models/example:generateContent", `{"candidates":[{"content":{"parts":[{"text":"hidden","thought":true},{"text":"hello"}]}}]}`, "x-goog-api-key"},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			key := "test-only-credential"
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != test.path {
					t.Errorf("path %q", r.URL.Path)
				}
				expected := key
				if test.header == "Authorization" {
					expected = "Bearer " + key
				}
				if r.Header.Get(test.header) != expected {
					t.Error("wrong authentication header")
				}
				if test.provider != "openai" && r.Header.Get("Authorization") != "" {
					t.Error("unexpected bearer credential")
				}
				if test.provider == "anthropic" && r.Header.Get("anthropic-version") == "" {
					t.Error("missing API version")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid body")
				}
				switch test.provider {
				case "openai":
					if body["max_completion_tokens"] != float64(33) {
						t.Error("token limit ignored")
					}
				case "anthropic":
					if body["max_tokens"] != float64(33) {
						t.Error("missing token limit")
					}
				case "gemini":
					if body["contents"] == nil {
						t.Error("missing contents")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, test.response)
			}))
			defer server.Close()
			env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL + "/custom/v1/", APIKeyEnv: "TEST_KEY", Model: "example"}, "", test.provider))
			env["TEST_KEY"] = key
			code, out, errOut := runTest(t, env, "", "--max-tokens", "33", "text", "generate", "--prompt", "hello")
			if code != 0 || out != "hello\n" || errOut != "" || calls != 1 {
				t.Fatalf("code=%d output=%q errors=%q calls=%d", code, out, errOut, calls)
			}
		})
	}
}
func TestConfigPrecedenceAndProviderIsolation(t *testing.T) {
	c := connectionConfig(Channel{APIKey: "inline", APIKeyEnv: "TEAM_KEY", BaseURL: "https://original.example/proxy/v1", BaseURLEnv: "TEAM_URL"}, "", "anthropic")
	env := map[string]string{"TEAM_KEY": "override", "TEAM_URL": "https://custom.example/prefix/v1", "OPENAI_API_KEY": "wrong-provider"}
	r, err := resolve(c, "text", func(k string) string { return env[k] })
	if err != nil || r.APIKey != "override" || r.BaseURL != env["TEAM_URL"] || r.KeySource != "environment" {
		t.Fatal("environment precedence failed", err)
	}
	r, err = resolve(connectionConfig(Channel{}, "", "gemini"), "text", func(k string) string { return env[k] })
	if err != nil || r.APIKey != "" {
		t.Fatal("cross-provider credential leak", err)
	}
	for _, raw := range []string{"ftp://example.com", "https://user:password@example.com/v1", "https://example.com/v1?key=secret", "https://example.com/#fragment"} {
		if validAPIURL(raw) {
			t.Fatal("invalid URL accepted")
		}
	}
}
func TestConfigInitAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "config.json")
	env := map[string]string{"NMODEL_CONFIG": path}
	code, _, errOut := runTest(t, env, "", "config", "init")
	if code != 0 {
		t.Fatal(errOut)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("configuration is not private")
	}
	code, _, _ = runTest(t, env, "", "config", "init")
	if code == 0 {
		t.Fatal("existing configuration overwritten")
	}
	c := exampleConfig()
	channel := c.Channels["openai"]
	channel.APIKey = "secret-value-to-redact"
	c.Channels["openai"] = channel
	b, _ := json.Marshal(c)
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runTest(t, env, "", "config", "show", "--json")
	if code != 0 || !json.Valid([]byte(out)) || strings.Contains(out, channel.APIKey) || !strings.Contains(out, "REDACTED") {
		t.Fatal("configuration leaked a key", errOut)
	}
}
func TestDryRunHasNoNetworkOrCredentials(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL + "/v1", APIKey: "private-key", Model: "example"}, "", "openai"))
	code, out, errOut := runTest(t, env, "", "text", "generate", "--prompt", "hello", "--dry-run", "--json")
	if code != 0 || calls.Load() != 0 || !json.Valid([]byte(out)) || strings.Contains(out, "private-key") {
		t.Fatal("dry run contacted service or leaked credential", errOut)
	}
	code, out, errOut = runTest(t, env, "", "models", "--api-key", "secret")
	if code == 0 || strings.Contains(out+errOut, "secret") {
		t.Fatal("credential command argument accepted or echoed")
	}
}
func TestImageOutputAndCredentialFreeDownload(t *testing.T) {
	var leaked bool
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" || r.Header.Get("x-goog-api-key") != ""
		io.WriteString(w, "image-data")
	}))
	defer media.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"url": media.URL + "/file?signature=example"}}})
	}))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL + "/v1", APIKey: "local-test", Model: "image"}, "", "openai"))
	path := filepath.Join(t.TempDir(), "image.png")
	code, _, errOut := runTest(t, env, "", "image", "generate", "--prompt", "cat", "-o", path)
	if code != 0 || leaked {
		t.Fatal("download failed or leaked credential", errOut)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "image-data" {
		t.Fatal("image not saved")
	}
	code, _, _ = runTest(t, env, "", "image", "generate", "--prompt", "cat", "-o", path)
	if code == 0 {
		t.Fatal("existing image overwritten")
	}
	a := app{client: newClient()}
	other := filepath.Join(t.TempDir(), "base64.png")
	data, _ := json.Marshal(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString([]byte("base64-image"))}}})
	if err := a.saveImage(context.Background(), data, other); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(other)
	if string(b) != "base64-image" {
		t.Fatal("base64 image not decoded")
	}
}
func TestVideoTaskAndDownload(t *testing.T) {
	var polled atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/videos":
			io.WriteString(w, `{"id":"task_1","status":"queued"}`)
		case "/v1/videos/task_1":
			polled.Add(1)
			io.WriteString(w, `{"id":"task_1","status":"completed"}`)
		case "/v1/videos/task_1/content":
			io.WriteString(w, "video-bytes")
		default:
			t.Error("unexpected endpoint")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL + "/v1", APIKey: "local-test", Model: "video"}, "", "openai"))
	path := filepath.Join(t.TempDir(), "movie.mp4")
	code, out, errOut := runTest(t, env, "", "video", "generate", "--prompt", "cat", "--poll-interval", "1s", "-o", path, "--json")
	b, _ := os.ReadFile(path)
	if code != 0 || !json.Valid([]byte(out)) || errOut != "" || string(b) != "video-bytes" || polled.Load() != 1 {
		t.Fatal("video lifecycle failed", errOut)
	}
	code, _, _ = runTest(t, env, "", "video", "status", "../unsafe")
	if code == 0 {
		t.Fatal("unsafe task ID accepted")
	}
}
func TestErrorsAreJSONAndDoNotEchoUpstreamSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret-from-upstream", 401) }))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL, APIKey: "local-test"}, "", "openai"))
	code, out, errOut := runTest(t, env, "", "models", "--json")
	if code != 1 || out != "" || !json.Valid([]byte(errOut)) || strings.Contains(errOut, "secret-from-upstream") {
		t.Fatal("error protocol failed")
	}
}
func TestRawRequestAndStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/v1/embeddings" {
			t.Error("lost URL prefix")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"input":"hello","model":"example"}` {
			t.Error("request body changed")
		}
		io.WriteString(w, `{"data":[]}`)
	}))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL + "/proxy/v1", APIKey: "local-test"}, "", "openai"))
	code, out, errOut := runTest(t, env, `{"input":"hello","model":"example"}`, "request", "POST", "/embeddings", "--data", "-", "--json")
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatal("raw request failed", errOut)
	}
}

func TestTextJSONWithOutputAndHelpLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"saved text"}}]}`)
	}))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL, APIKey: "local-test", Model: "text"}, "zh", "openai"))
	path := filepath.Join(t.TempDir(), "text.txt")
	code, out, errOut := runTest(t, env, "", "text", "generate", "--prompt", "hello", "--json", "-o", path)
	b, _ := os.ReadFile(path)
	if code != 0 || !json.Valid([]byte(out)) || string(b) != "saved text" {
		t.Fatal("JSON output skipped file writing", errOut)
	}
	code, out, _ = runTest(t, env, "", "help")
	if code != 0 || !strings.Contains(out, "命令行") {
		t.Fatal("configured help language ignored")
	}
	code, out, errOut = runTest(t, env, "", "--unknown", "--json")
	if code != 1 || out != "" || !json.Valid([]byte(errOut)) {
		t.Fatal("invalid option did not produce JSON error")
	}
}
func TestRedirectNeverForwardsKey(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: origin.URL, APIKey: "local-test"}, "", "openai"))
	code, _, _ := runTest(t, env, "", "models")
	if code != 1 || calls.Load() != 0 {
		t.Fatal("followed credential-bearing API redirect")
	}
}
func TestGeminiImageEditAndOpenAIMultipart(t *testing.T) {
	// A minimal PNG signature is sufficient for MIME sniffing in a transport contract test.
	image := filepath.Join(t.TempDir(), "reference.png")
	if err := os.WriteFile(image, append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, make([]byte, 24)...), 0600); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"gemini", "openai"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if provider == "openai" {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
					}
					if r.FormValue("prompt") != "edit" || r.FormValue("model") != "image" {
						t.Error("multipart fields missing")
					}
					file, _, err := r.FormFile("image")
					if err != nil {
						t.Error(err)
					} else {
						file.Close()
					}
					io.WriteString(w, `{"data":[{"b64_json":"aW1hZ2U="}]}`)
				} else {
					var payload map[string]any
					if json.NewDecoder(r.Body).Decode(&payload) != nil {
						t.Error("invalid Gemini body")
					}
					if payload["generationConfig"] == nil {
						t.Error("missing image generation config")
					}
					io.WriteString(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"aW1hZ2U=","mimeType":"image/png"}}]}}]}`)
				}
			}))
			defer server.Close()
			env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL, APIKey: "local-test", Model: "image"}, "", provider))
			path := filepath.Join(t.TempDir(), "edited.png")
			code, _, errOut := runTest(t, env, "", "image", "edit", "--prompt", "edit", "--file", image, "-o", path)
			b, _ := os.ReadFile(path)
			if code != 0 || string(b) != "image" {
				t.Fatal("image edit failed", errOut)
			}
		})
	}
}

func TestConfiguredTokenParameterAndChineseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid JSON")
		}
		if body["max_tokens"] != float64(1024) || body["max_completion_tokens"] != nil {
			t.Error("configured token parameter ignored")
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"hello"}}]}`)
	}))
	defer server.Close()
	env := envConfig(t, connectionConfig(Channel{BaseURL: server.URL, APIKeyEnv: "TEST_KEY", Model: "example", MaxTokensField: "max_tokens"}, "zh", "openai"))
	code, _, errOut := runTest(t, env, "", "text", "generate", "--prompt", "hello")
	if code != 1 || !strings.Contains(errOut, "密钥环境变量") {
		t.Fatal("Chinese error message missing")
	}
	env["TEST_KEY"] = "test-only-key"
	code, _, errOut = runTest(t, env, "", "text", "generate", "--prompt", "hello")
	if code != 0 {
		t.Fatal("compatible token field failed", errOut)
	}
}

func connectionConfig(channel Channel, language, protocol string) Config {
	channel.Provider = protocol
	return Config{Version: 1, Language: language, Channels: map[string]Channel{"test": channel}, Routes: map[string]Route{"text": {Channel: "test"}, "image": {Channel: "test"}, "video": {Channel: "test"}}}
}

func TestCapabilityRoutingAndSharedChannelModels(t *testing.T) {
	var textCalls, imageCalls, videoCalls atomic.Int32
	textServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		textCalls.Add(1)
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "claude-key" {
			t.Error("wrong text route or key")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "text-model" {
			t.Error("wrong text model")
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"hello"}]}`)
	}))
	defer textServer.Close()
	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer media-key" {
			t.Error("channel key crossed routes")
		}
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/images/generations":
			imageCalls.Add(1)
			if body["model"] != "image-model" {
				t.Error("wrong image model")
			}
			io.WriteString(w, `{"data":[{"b64_json":"aW1hZ2U="}]}`)
		case "/v1/videos":
			videoCalls.Add(1)
			if body["model"] != "video-model" {
				t.Error("wrong video model")
			}
			io.WriteString(w, `{"id":"video_task","status":"queued"}`)
		default:
			t.Error("wrong media endpoint")
			http.NotFound(w, r)
		}
	}))
	defer mediaServer.Close()
	c := Config{Version: 1, Channels: map[string]Channel{
		"claude": {Provider: "anthropic", BaseURL: textServer.URL, APIKeyEnv: "CLAUDE_KEY"},
		"media":  {Provider: "openai", BaseURL: mediaServer.URL, APIKeyEnv: "MEDIA_KEY"},
	}, Routes: map[string]Route{"text": {Channel: "claude", Model: "text-model"}, "image": {Channel: "media", Model: "image-model"}, "video": {Channel: "media", Model: "video-model"}}}
	env := envConfig(t, c)
	env["CLAUDE_KEY"] = "claude-key"
	env["MEDIA_KEY"] = "media-key"
	env["NMODEL_API_KEY"] = "must-not-override"
	for _, args := range [][]string{
		{"text", "generate", "--prompt", "hello"},
		{"image", "generate", "--prompt", "cat", "--json"},
		{"video", "generate", "--prompt", "cat", "--json"},
		{"models", "--type", "image", "--json"},
	} {
		if code, _, errors := runTest(t, env, "", args...); code != 0 {
			t.Fatal("capability command failed", errors)
		}
	}
	if textCalls.Load() != 1 || imageCalls.Load() != 1 || videoCalls.Load() != 1 {
		t.Fatal("commands did not follow capability routes")
	}
	if code, _, _ := runTest(t, env, "", "image", "generate", "--type", "text", "--prompt", "cat", "--json"); code == 0 {
		t.Fatal("conflicting capability flag accepted")
	}
	delete(c.Channels, "claude")
	if _, err := resolve(c, "text", func(string) string { return "" }); err == nil {
		t.Fatal("missing channel accepted")
	}
	delete(c.Routes, "image")
	if _, err := resolve(c, "image", func(string) string { return "" }); err == nil {
		t.Fatal("missing route accepted")
	}
}
