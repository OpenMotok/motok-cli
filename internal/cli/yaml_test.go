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
	env := map[string]string{"NMODEL_CONFIG": path}
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
			env := map[string]string{"NMODEL_CONFIG": path}
			code, _, errors := runTest(t, env, "", "config", "init")
			if code != 0 {
				t.Fatal(errors)
			}
			c, err := readConfig(path)
			if err != nil || len(c.Channels) != 4 || c.Routes["video"].Channel != "media" {
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
	dir := filepath.Join(root, "nmodel")
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
		if key == "NMODEL_CONFIG" {
			return legacy
		}
		return ""
	})
	if path != legacy {
		t.Fatal("explicit configuration path was ignored")
	}
}

func TestChannelProviderIsProtocolAndRouteChannelIsReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `channels:
  company-proxy:
    provider: anthropic
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
	env := map[string]string{"NMODEL_CONFIG": path}
	code, out, errors := runTest(t, env, "", "text", "generate", "--prompt", "hello", "--dry-run", "--json")
	if code != 0 {
		t.Fatal(errors)
	}
	var preview map[string]any
	if err = json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if preview["channel"] != "company-proxy" || preview["provider"] != "anthropic" || preview["url"] != "https://example.com/anthropic/v1/messages" {
		t.Fatal("preview does not distinguish reference and protocol")
	}
	if _, exists := preview["protocol"]; exists {
		t.Fatal("unexpected protocol field emitted")
	}
}
