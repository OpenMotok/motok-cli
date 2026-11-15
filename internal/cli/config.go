package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Channel struct {
	Protocol       string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	BaseURL        string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	BaseURLEnv     string `json:"base_url_env,omitempty" yaml:"base_url_env,omitempty"`
	APIKey         string `json:"api_key" yaml:"api_key"`
	APIKeyEnv      string `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
	Model          string `json:"model,omitempty" yaml:"model,omitempty"`
	MaxTokensField string `json:"max_tokens_field,omitempty" yaml:"max_tokens_field,omitempty"`
}

// channelFields encodes a Channel with an always-present api_key so new files
// show where to paste a key. envKeyChannel drops the empty api_key when the key
// comes from api_key_env, so the file does not suggest storing it in plain text.
type channelFields Channel
type envKeyChannel struct {
	Protocol       string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	BaseURL        string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	BaseURLEnv     string `json:"base_url_env,omitempty" yaml:"base_url_env,omitempty"`
	APIKey         string `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	APIKeyEnv      string `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
	Model          string `json:"model,omitempty" yaml:"model,omitempty"`
	MaxTokensField string `json:"max_tokens_field,omitempty" yaml:"max_tokens_field,omitempty"`
}

func (c Channel) encoded() any {
	if c.APIKey == "" && c.APIKeyEnv != "" {
		return envKeyChannel(c)
	}
	return channelFields(c)
}
func (c Channel) MarshalYAML() (any, error)    { return c.encoded(), nil }
func (c Channel) MarshalJSON() ([]byte, error) { return json.Marshal(c.encoded()) }

type Route struct {
	Channel string `json:"channel" yaml:"channel"`
	Model   string `json:"model,omitempty" yaml:"model,omitempty"`
}
type Config struct {
	Version  int                `json:"version" yaml:"version"`
	Language string             `json:"language,omitempty" yaml:"language,omitempty"`
	Channels map[string]Channel `json:"channels" yaml:"channels"`
	Routes   map[string]Route   `json:"routes" yaml:"routes"`
}
type resolved struct {
	Channel
	ChannelName, Kind, KeySource, Protocol string
}

func configPath(getenv func(string) string) (string, error) {
	if p := getenv("MOTOK_CONFIG"); p != "" {
		return p, nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", failure("config_path", "cannot locate the user configuration directory")
	}
	for _, name := range []string{"config.yaml", "config.yml", "config.json"} {
		path := filepath.Join(root, "motok", name)
		if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}
	return filepath.Join(root, "motok", "config.yaml"), nil
}
func readConfig(path string) (Config, error) {
	c := Config{Version: 1}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return c, failure("config_read", "cannot read configuration")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return c, failure("config_read", "configuration exceeds 1 MiB or cannot be read")
	}
	var decodeErr, trailingErr error
	if strings.EqualFold(filepath.Ext(path), ".json") {
		d := json.NewDecoder(strings.NewReader(string(data)))
		d.DisallowUnknownFields()
		decodeErr = d.Decode(&c)
		if decodeErr == nil {
			trailingErr = d.Decode(new(any))
		}
	} else {
		d := yaml.NewDecoder(strings.NewReader(string(data)))
		d.KnownFields(true)
		decodeErr = d.Decode(&c)
		if decodeErr == nil {
			trailingErr = d.Decode(new(any))
		}
	}
	if decodeErr != nil || trailingErr != io.EOF {
		return c, failure("config_invalid", "configuration must contain one valid YAML or JSON document with known fields")
	}
	if c.Version != 1 || c.Channels == nil || c.Routes == nil {
		return c, failure("config_invalid", "configuration requires version 1, channels and routes")
	}
	if c.Language != "" && c.Language != "en" && c.Language != "zh" {
		return c, failure("config_invalid", "language must be en or zh")
	}
	return c, nil
}
func resolve(c Config, kind string, getenv func(string) string) (resolved, error) {
	if kind != "text" && kind != "image" && kind != "video" {
		return resolved{}, failure("route_invalid", "type must be text, image or video")
	}
	route, ok := c.Routes[kind]
	if !ok || route.Channel == "" {
		return resolved{}, failure("route_missing", "configure the requested capability in routes")
	}
	p, ok := c.Channels[route.Channel]
	if !ok {
		return resolved{}, failure("channel_missing", "route references a channel that is not configured")
	}
	protocol := p.Protocol
	if protocol == "" {
		protocol = "openai"
	}
	if route.Model != "" {
		p.Model = route.Model
	}
	if model := getenv("MOTOK_" + strings.ToUpper(kind) + "_MODEL"); model != "" {
		p.Model = model
	}
	defaults := map[string][3]string{
		"openai":    {"https://api.openai.com/v1", "OPENAI_API_KEY", "OPENAI_BASE_URL"},
		"anthropic": {"https://api.anthropic.com/v1", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"},
		"gemini":    {"https://generativelanguage.googleapis.com/v1beta", "GEMINI_API_KEY", "GEMINI_BASE_URL"},
	}
	def, ok := defaults[protocol]
	if !ok {
		return resolved{}, failure("protocol_invalid", "protocol must be openai, anthropic or gemini")
	}
	if p.APIKeyEnv == "" {
		p.APIKeyEnv = def[1]
	}
	if p.BaseURLEnv == "" {
		p.BaseURLEnv = def[2]
	}
	if p.MaxTokensField != "" && p.MaxTokensField != "max_tokens" && p.MaxTokensField != "max_completion_tokens" {
		return resolved{}, failure("config_invalid", "max_tokens_field must be max_tokens or max_completion_tokens")
	}
	source := "none"
	if p.APIKey != "" {
		source = "config"
	}
	if key := getenv(p.APIKeyEnv); key != "" {
		p.APIKey = key
		source = "environment"
	}
	if u := getenv(p.BaseURLEnv); u != "" {
		p.BaseURL = u
	}
	if p.BaseURL == "" {
		p.BaseURL = def[0]
	}
	if !validAPIURL(p.BaseURL) {
		return resolved{}, failure("url_invalid", "base_url must be an HTTP(S) URL without credentials, query or fragment")
	}
	u, _ := url.Parse(p.BaseURL)
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
	if u.Path == "" || u.Path == "/" {
		if protocol == "gemini" {
			p.BaseURL += "/v1beta"
		} else {
			p.BaseURL += "/v1"
		}
	}
	if strings.ContainsAny(p.APIKey, "\r\n\x00") || strings.TrimSpace(p.APIKey) != p.APIKey {
		return resolved{}, failure("key_invalid", "API key contains invalid whitespace")
	}
	return resolved{Channel: p, ChannelName: route.Channel, Kind: kind, KeySource: source, Protocol: protocol}, nil
}
func validAPIURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Opaque == ""
}
func localHost(host string) bool {
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
func encodeConfig(path string, c Config) ([]byte, error) {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		data, err := json.MarshalIndent(c, "", "  ")
		return append(data, '\n'), err
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(c); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func writeConfig(path string, c Config) error {
	data, err := encodeConfig(path, c)
	if err != nil {
		return failure("config_write", "cannot encode configuration")
	}
	return writeNew(path, data)
}
func defaultConfig() Config {
	return Config{Version: 1, Channels: map[string]Channel{"openai": {Protocol: "openai"}}, Routes: map[string]Route{"text": {Channel: "openai"}, "image": {Channel: "openai"}, "video": {Channel: "openai"}}}
}

// Default Motok model IDs written by init: Seedream 5.5 and Seedance 2.0 Mini.
const (
	defaultImageModel = "dola-seedream-5-5-261028"
	defaultVideoModel = "dreamina-seedance-2-0-mini-260615"
)

func exampleConfig() Config {
	return Config{
		Version:  1,
		Language: "en",
		Channels: map[string]Channel{"motok": {Protocol: "openai", BaseURL: "https://YOUR_API_HOST/v1", APIKey: ""}},
		Routes: map[string]Route{
			"text":  {Channel: "motok", Model: "YOUR_TEXT_MODEL_ID"},
			"image": {Channel: "motok", Model: defaultImageModel},
			"video": {Channel: "motok", Model: defaultVideoModel},
		},
	}
}
