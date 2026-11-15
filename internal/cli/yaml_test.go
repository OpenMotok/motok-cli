package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestYAMLConfigurationAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `# Configuration comments are supported.
language: zh
channels:
  official:
    base_url: https://example.com/proxy/v1
    api_key: private-test-value
    api_key_env: TEAM_API_KEY
routes:
  text:
    channel: official
    model: text-model
  image:
    channel: official
    model: image-model
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Routes["image"].Model != "image-model" {
		t.Fatal("YAML routes or default version not decoded")
	}
	r, err := resolve(c, "text", func(key string) string {
		if key == "TEAM_API_KEY" {
			return "environment-value"
		}
		return ""
	})
	if err != nil || r.APIKey != "environment-value" || r.BaseURL != "https://example.com/proxy/v1" {
		t.Fatal("YAML environment override failed", err)
	}
	env := map[string]string{"MOTOK_CONFIG": path}
	for _, args := range [][]string{{"config", "show"}, {"config", "show", "--json"}} {
		code, out, errors := runTest(t, env, "", args...)
		if code != 0 || strings.Contains(out, "private-test-value") || !strings.Contains(out, "REDACTED") {
			t.Fatal("YAML show leaked credentials", errors)
		}
		if len(args) == 3 && !json.Valid([]byte(out)) {
			t.Fatal("JSON show mode failed")
		}
	}
}
func TestYAMLRejectsUnknownDuplicateAndExtraDocuments(t *testing.T) {
	base := "version: 1\nchannels:\n  test:\n    api_key_env: TEST_KEY\nroutes:\n  text:\n    channel: test\n"
	cases := []string{
		base + "unexpected: true\n",
		strings.Replace(base, "api_key_env: TEST_KEY", "api_key_env: TEST_KEY\n    api_keey: secret", 1),
		strings.Replace(base, "channel: test", "channel: test\n    modle: typo", 1),
		base + "language: en\nlanguage: zh\n",
		base + "---\nversion: 1\n",
		"channels: [\n",
	}
	for i, data := range cases {
		path := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readConfig(path); err == nil {
			t.Fatalf("invalid YAML case %d accepted", i)
		}
	}
}
func TestYAMLInitRoundTripAndExistingFile(t *testing.T) {
	for _, extension := range []string{"yaml", "yml", "json"} {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+extension)
			env := map[string]string{"MOTOK_CONFIG": path}
			code, _, errors := runTest(t, env, "", "config", "init")
			if code != 0 {
				t.Fatal(errors)
			}
			c, err := readConfig(path)
			if err != nil || len(c.Channels) != 1 || c.Routes["video"].Channel != "motok" {
				t.Fatal("configuration did not round-trip", err)
			}
			data, _ := os.ReadFile(path)
			if extension == "json" && !json.Valid(data) {
				t.Fatal("JSON init compatibility failed")
			}
			if extension != "json" && !strings.Contains(string(data), "channels:") {
				t.Fatal("init did not write YAML")
			}
			if code, _, _ := runTest(t, env, "", "config", "init"); code == 0 {
				t.Fatal("existing configuration overwritten")
			}
		})
	}
}

func TestDefaultConfigDiscoveryPrefersYAMLAndRetainsJSON(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("APPDATA", temp)
	root, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := configPath(func(string) string { return "" })
	if err != nil || filepath.Base(path) != "config.yaml" {
		t.Fatal("default must use YAML", err)
	}
	dir := filepath.Join(root, "motok")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "config.json")
	if err = writeConfig(legacy, exampleConfig()); err != nil {
		t.Fatal(err)
	}
	path, err = configPath(func(string) string { return "" })
	if err != nil || path != legacy {
		t.Fatal("existing JSON config was ignored", err)
	}
	yml := filepath.Join(dir, "config.yml")
	if err = writeConfig(yml, exampleConfig()); err != nil {
		t.Fatal(err)
	}
	path, _ = configPath(func(string) string { return "" })
	if path != yml {
		t.Fatal("YML config precedence failed")
	}
	preferred := filepath.Join(dir, "config.yaml")
	if err = writeConfig(preferred, exampleConfig()); err != nil {
		t.Fatal(err)
	}
	path, _ = configPath(func(string) string { return "" })
	if path != preferred {
		t.Fatal("YAML config precedence failed")
	}
	path, _ = configPath(func(key string) string {
		if key == "MOTOK_CONFIG" {
			return legacy
		}
		return ""
	})
	if path != legacy {
		t.Fatal("explicit configuration path was ignored")
	}
}

func TestChannelProtocolIsProtocolAndRouteChannelIsReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `channels:
  company-proxy:
    protocol: anthropic
    base_url: https://example.com/anthropic/v1
    api_key_env: COMPANY_KEY
routes:
  text:
    channel: company-proxy
    model: custom-model
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := resolve(c, "text", func(key string) string {
		if key == "COMPANY_KEY" {
			return "test-only-key"
		}
		return ""
	})
	if err != nil || r.ChannelName != "company-proxy" || r.Protocol != "anthropic" || r.Model != "custom-model" {
		t.Fatal("protocol and channel reference were confused", err)
	}
	env := map[string]string{"MOTOK_CONFIG": path}
	code, out, errors := runTest(t, env, "", "text", "generate", "--prompt", "hello", "--dry-run", "--json")
	if code != 0 {
		t.Fatal(errors)
	}
	var preview map[string]any
	if err = json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if preview["channel"] != "company-proxy" || preview["protocol"] != "anthropic" || preview["url"] != "https://example.com/anthropic/v1/messages" {
		t.Fatal("preview does not distinguish reference and protocol")
	}
	if _, exists := preview["provider"]; exists {
		t.Fatal("obsolete provider field emitted")
	}
}

func TestProtocolDefaultsAndRejectsProvider(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+format)
			raw := `channels:
  media:
    base_url: https://example.com/v1
routes:
  video:
    channel: media
`
			if format == "json" {
				raw = `{"channels":{"media":{"base_url":"https://example.com/v1"}},"routes":{"video":{"channel":"media"}}}`
			}
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := readConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			r, err := resolve(c, "video", func(string) string { return "" })
			if err != nil || r.Protocol != "openai" {
				t.Fatal("omitted protocol must default to openai", r.Protocol, err)
			}
			if format == "json" {
				raw = `{"channels":{"media":{"provider":"openai"}},"routes":{"video":{"channel":"media"}}}`
			} else {
				raw = "channels:\n  media:\n    provider: openai\nroutes:\n  video:\n    channel: media\n"
			}
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readConfig(path); err == nil {
				t.Fatal("obsolete provider accepted")
			}
		})
	}
}

func TestMotokInitCreatesBasicPrivateConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	env := map[string]string{"MOTOK_CONFIG": path, "MOTOK_BASE_URL": "https://gateway.example/v1"}
	code, _, stderr := runTest(t, env, "", "init")
	if code != 0 {
		t.Fatal(stderr)
	}
	c, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Channels) != 1 || c.Channels["motok"].Protocol != "openai" || c.Channels["motok"].BaseURL != "https://gateway.example/v1" || c.Channels["motok"].APIKey != "" {
		t.Fatal("unexpected Motok defaults")
	}
	for _, kind := range []string{"text", "image", "video"} {
		if c.Routes[kind].Channel != "motok" || c.Routes[kind].Model == "" {
			t.Fatal("missing route", kind)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `api_key: ""`) {
		t.Fatal("missing editable key field")
	}
	if code, _, _ := runTest(t, env, "", "init"); code == 0 {
		t.Fatal("init overwrote an existing file")
	}
	if code, _, _ := runTest(t, env, "", "config", "init"); code == 0 {
		t.Fatal("alias overwrote an existing file")
	}
}

func TestMotokInitReferencesKeyEnvironment(t *testing.T) {
	for _, name := range []string{"config.yaml", "config.json"} {
		path := filepath.Join(t.TempDir(), name)
		env := map[string]string{"MOTOK_CONFIG": path, "MOTOK_API_KEY": "sk-init-secret"}
		if code, _, stderr := runTest(t, env, "", "init"); code != 0 {
			t.Fatal(stderr)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "sk-init-secret") || strings.Contains(string(raw), "api_key\"") || strings.Contains(string(raw), "api_key:") {
			t.Fatal("init stored or suggested a plain-text key", name)
		}
		c, err := readConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if c.Channels["motok"].APIKeyEnv != "MOTOK_API_KEY" || c.Routes["image"].Model != defaultImageModel || c.Routes["video"].Model != defaultVideoModel {
			t.Fatal("unexpected init defaults", name)
		}
		code, out, stderr := runTest(t, env, "", "auth", "status", "--json")
		if code != 0 || !strings.Contains(out, `"key_source":"environment"`) {
			t.Fatal("init key reference not resolved", out, stderr)
		}
	}
}

func TestConfigPathDoesNotReadOrCreateConfiguration(t *testing.T) {
	for _, exists := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if exists {
			if err := os.WriteFile(path, []byte("invalid: ["), 0600); err != nil {
				t.Fatal(err)
			}
		}
		env := map[string]string{"MOTOK_CONFIG": path}
		code, out, stderr := runTest(t, env, "", "config", "path")
		if code != 0 || strings.TrimSpace(out) != path {
			t.Fatal("path lookup failed", code, out, stderr)
		}
		code, out, stderr = runTest(t, env, "", "config", "path", "--json")
		var response struct {
			Config string `json:"config"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &response) != nil || response.Config != path {
			t.Fatal("JSON path failed", out, stderr)
		}
		if !exists {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("path lookup created configuration")
			}
		}
	}
	code, out, stderr := runTest(t, map[string]string{"MOTOK_CONFIG": "relative-config.yaml"}, "", "config", "path")
	want, err := filepath.Abs("relative-config.yaml")
	if err != nil || code != 0 || strings.TrimSpace(out) != want {
		t.Fatal("relative path not resolved", out, stderr, err)
	}
}
