package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var Version = "0.1.0"

type options struct {
	kind, lang, model, prompt, output, data, file, size, id string
	json, dryRun, wait, help, version, ai                   bool
	timeout, poll                                           time.Duration
	maxTokens                                               int
	seconds                                                 string
}
type app struct {
	in          io.Reader
	out, errOut io.Writer
	getenv      func(string) string
	connection  resolved
	opts        options
	client      *http.Client
}

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, getenv func(string) string) int {
	a := &app{in: in, out: out, errOut: errOut, getenv: getenv, client: newClient()}
	commands, err := a.parse(args)
	if err == nil {
		err = a.run(ctx, commands)
	}
	if err == nil {
		return 0
	}
	var e *cliError
	if !errors.As(err, &e) {
		e = &cliError{Code: "operation_failed", Message: "operation failed"}
	}
	if a.opts.json {
		_ = json.NewEncoder(errOut).Encode(map[string]any{"error": e})
	} else {
		prefix := "Error"
		if a.opts.lang == "zh" {
			prefix = "错误"
		}
		message := e.Error()
		if a.opts.lang == "zh" {
			message = e.Code + ": " + chineseError(e)
		}
		fmt.Fprintf(errOut, "%s: %s\n", prefix, message)
	}
	return 1
}
func (a *app) parse(args []string) ([]string, error) {
	f := flag.NewFlagSet("nmodel", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&a.opts.kind, "type", "", "text, image or video")
	f.StringVar(&a.opts.lang, "lang", "", "en or zh")
	f.StringVar(&a.opts.model, "model", "", "model")
	f.StringVar(&a.opts.prompt, "prompt", "", "prompt; - reads stdin")
	f.StringVar(&a.opts.output, "output", "", "output file")
	f.StringVar(&a.opts.output, "o", "", "output file")
	f.StringVar(&a.opts.data, "data", "", "JSON file; - reads stdin")
	f.StringVar(&a.opts.file, "file", "", "reference image")
	f.StringVar(&a.opts.size, "size", "", "image or video size")
	f.StringVar(&a.opts.seconds, "seconds", "", "video duration")
	f.IntVar(&a.opts.maxTokens, "max-tokens", 1024, "maximum output tokens")
	f.DurationVar(&a.opts.timeout, "timeout", 10*time.Minute, "overall timeout")
	f.DurationVar(&a.opts.poll, "poll-interval", 10*time.Second, "video polling interval")
	f.BoolVar(&a.opts.json, "json", false, "JSON output")
	f.BoolVar(&a.opts.dryRun, "dry-run", false, "inspect without network calls")
	f.BoolVar(&a.opts.wait, "wait", false, "wait for video")
	f.BoolVar(&a.opts.help, "help", false, "help")
	f.BoolVar(&a.opts.help, "h", false, "help")
	f.BoolVar(&a.opts.version, "version", false, "version")
	f.BoolVar(&a.opts.version, "v", false, "version")
	f.BoolVar(&a.opts.ai, "ai", false, "agent help")
	// Standard flag parsing stops at the command. Move known options first so options work on either side.
	var flags, commands []string
	var optionError error
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			commands = append(commands, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			commands = append(commands, arg)
			continue
		}
		name := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
		option := f.Lookup(name)
		if option == nil {
			optionError = failure("option_unknown", "unknown option; use nmodel help (credentials and URLs belong in config or environment)")
			continue
		}
		flags = append(flags, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
		if ok && boolean.IsBoolFlag() {
			continue
		}
		i++
		if i == len(args) {
			return nil, failure("option_value", "option requires a value")
		}
		flags = append(flags, args[i])
	}
	if err := f.Parse(flags); err != nil {
		return nil, failure("option_invalid", "invalid option value")
	}
	if optionError != nil {
		return nil, optionError
	}
	if a.opts.lang != "" && a.opts.lang != "en" && a.opts.lang != "zh" {
		return nil, failure("language_invalid", "lang must be en or zh")
	}
	if a.opts.timeout <= 0 || a.opts.poll < time.Second || a.opts.maxTokens < 1 {
		return nil, failure("option_invalid", "timeout and max-tokens must be positive; poll-interval must be at least 1s")
	}
	return commands, nil
}
func (a *app) run(ctx context.Context, cmd []string) error {
	if a.opts.lang == "" {
		a.opts.lang = a.getenv("NMODEL_LANG")
	}
	if a.opts.version {
		return a.print(map[string]string{"version": Version}, Version)
	}
	if len(cmd) == 0 || cmd[0] == "help" || a.opts.help {
		return a.help()
	}
	path, err := configPath(a.getenv)
	if err != nil {
		return err
	}
	if strings.Join(cmd, " ") == "config init" {
		if err = writeConfig(path, exampleConfig()); err != nil {
			return err
		}
		return a.print(map[string]string{"config": path}, path)
	}
	c, err := readConfig(path)
	if err != nil {
		return err
	}
	if a.opts.lang == "" {
		a.opts.lang = a.getenv("NMODEL_LANG")
		if a.opts.lang == "" {
			a.opts.lang = c.Language
		}
		if a.opts.lang == "" {
			a.opts.lang = "en"
		}
	}
	if a.opts.lang != "en" && a.opts.lang != "zh" {
		return failure("language_invalid", "language must be en or zh")
	}
	if strings.Join(cmd, " ") == "config show" {
		for name, channel := range c.Channels {
			if channel.APIKey != "" {
				channel.APIKey = "[REDACTED]"
			}
			c.Channels[name] = channel
		}
		if a.opts.json {
			return a.print(c, "")
		}
		data, err := encodeConfig(path, c)
		if err != nil {
			return failure("config_write", "cannot encode configuration")
		}
		_, err = a.out.Write(data)
		return err
	}
	kind := a.opts.kind
	inferred := ""
	if len(cmd) > 0 {
		switch cmd[0] {
		case "text", "response":
			inferred = "text"
		case "image":
			inferred = "image"
		case "video":
			inferred = "video"
		}
	}
	if inferred != "" {
		if kind != "" && kind != inferred {
			return failure("route_invalid", "--type conflicts with the command capability")
		}
		kind = inferred
	}
	if kind == "" {
		kind = "text"
	}
	a.connection, err = resolve(c, kind, a.getenv)
	if err != nil {
		return err
	}
	if a.opts.model == "" {
		a.opts.model = a.connection.Model
	}
	command := strings.Join(cmd, " ")
	if command == "auth status" {
		return a.print(map[string]any{"channel": a.connection.ChannelName, "type": a.connection.Kind, "provider": a.connection.Protocol, "base_url": a.connection.BaseURL, "configured": a.connection.APIKey != "", "key_source": a.connection.KeySource}, "")
	}
	ctx, cancel := context.WithTimeout(ctx, a.opts.timeout)
	defer cancel()
	switch command {
	case "models", "login", "status":
		if a.opts.dryRun {
			return a.preview("GET", "/models", "", nil)
		}
		data, err := a.jsonRequest(ctx, "GET", "/models", "", nil)
		if err != nil {
			return err
		}
		if command != "models" {
			return a.print(map[string]any{"channel": a.connection.ChannelName, "type": a.connection.Kind, "provider": a.connection.Protocol, "authenticated": true}, a.localized("Authentication successful", "鉴权成功"))
		}
		if a.opts.json {
			return a.print(data, "")
		}
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(data, &result) != nil {
			return failure("response_invalid", "invalid model list")
		}
		for _, m := range result.Data {
			fmt.Fprintln(a.out, m.ID)
		}
		for _, m := range result.Models {
			fmt.Fprintln(a.out, strings.TrimPrefix(m.Name, "models/"))
		}
		return nil
	case "text generate", "image generate", "image edit", "video generate", "response generate":
		return a.generate(ctx, command)
	}
	if len(cmd) == 3 && cmd[0] == "video" && (cmd[1] == "status" || cmd[1] == "download") {
		return a.video(ctx, cmd[1], cmd[2])
	}
	if len(cmd) == 3 && cmd[0] == "request" {
		return a.raw(ctx, cmd[1], cmd[2])
	}
	return failure("command_unknown", "unknown command; use nmodel help")
}
func (a *app) localized(en, zh string) string {
	if a.opts.lang == "zh" {
		return zh
	}
	return en
}
func (a *app) print(value any, human string) error {
	if !a.opts.json && human != "" {
		_, err := fmt.Fprintln(a.out, human)
		return err
	}
	enc := json.NewEncoder(a.out)
	if !a.opts.json {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(value)
}
func (a *app) preview(method, path, contentType string, body []byte) error {
	var payload any
	if json.Valid(body) {
		_ = json.Unmarshal(body, &payload)
	}
	return a.print(map[string]any{"dry_run": true, "channel": a.connection.ChannelName, "type": a.connection.Kind, "provider": a.connection.Protocol, "method": method, "url": a.connection.BaseURL + path, "content_type": contentType, "body": payload, "file": a.opts.file}, "")
}
func (a *app) help() error {
	if a.opts.lang == "" {
		if path, err := configPath(a.getenv); err == nil {
			if cfg, err := readConfig(path); err == nil {
				a.opts.lang = cfg.Language
			}
		}
	}

	if a.opts.json || a.opts.ai {
		return a.print(map[string]any{"name": "nmodel", "version": Version, "commands": []string{"config init", "config show", "auth status", "login", "models", "text generate", "response generate", "image generate", "image edit", "video generate", "video status ID", "video download ID", "request METHOD /endpoint"}, "auth": "Configuration file and environment variables only; no credentials in command flags.", "options": []string{"--type text|image|video", "--model ID", "--prompt TEXT|-", "--output FILE", "--file FILE", "--data FILE|-", "--size SIZE", "--seconds N", "--wait", "--poll-interval 10s", "--timeout 10m", "--json", "--dry-run", "--lang en|zh"}}, "")
	}
	text := `NModel CLI — direct official APIs and compatible gateways

  nmodel config init                       Create a private example configuration
  nmodel config show                       Show configuration with keys redacted
  nmodel auth status                       Show configured connection without networking
  nmodel login                             Validate configured API key via model discovery
  nmodel models                            List models available to the configured key
  nmodel text generate --prompt "Hello"     Generate text
  nmodel response generate --prompt "Hi"    OpenAI Responses API
  nmodel image generate --prompt "A cat" -o cat.png
  nmodel image edit --prompt "Watercolor" --file source.png -o edited.png
  nmodel video generate --prompt "A cat" --wait -o cat.mp4
  nmodel video status TASK_ID
  nmodel video download TASK_ID -o cat.mp4
  nmodel request POST /embeddings --data request.json --json

Configuration: NMODEL_CONFIG or the user config directory / nmodel / config.yaml.
Keys and URLs come only from config and environment. Commands select channels automatically using routes.text/image/video.
Providers: openai (also NModel / compatible services), anthropic, gemini.
Set a model on a route/channel or use --model. Model availability depends on the service.
Options: --type text|image|video --json --dry-run --lang en|zh --timeout 10m
Video options: --seconds N --size WIDTHxHEIGHT --wait --poll-interval 10s
--prompt - and --data - read standard input. Output files are never overwritten.
Use nmodel help --ai for machine-readable command discovery.
`
	if a.opts.lang == "zh" {
		text = `NModel 命令行 — 直连官方 API 与兼容网关

  nmodel config init                       生成权限受限的示例配置
  nmodel config show                       查看配置（隐藏密钥）
  nmodel auth status                       查看当前配置，不发送请求
  nmodel login                             使用模型列表接口验证密钥
  nmodel models                            列出可用模型
  nmodel text generate --prompt "你好"      生成文本
  nmodel response generate --prompt "你好"  调用 OpenAI Responses
  nmodel image generate --prompt "一只猫" -o cat.png
  nmodel image edit --prompt "改成水彩" --file source.png -o edited.png
  nmodel video generate --prompt "一只猫" --wait -o cat.mp4
  nmodel video status TASK_ID
  nmodel video download TASK_ID -o cat.mp4
  nmodel request POST /embeddings --data request.json --json

配置路径：NMODEL_CONFIG 或用户配置目录中的 nmodel/config.yaml。
API Key 和 URL 仅从配置文件与环境变量读取，通过 routes.text/image/video 自动选择渠道。
协议：openai（含 NModel 和兼容服务）、anthropic、gemini。
模型在 routes 或 channels 中设置，或使用 --model；可用能力取决于实际服务。
选项：--type text|image|video --json --dry-run --lang en|zh --timeout 10m
视频：--seconds N --size WIDTHxHEIGHT --wait --poll-interval 10s
--prompt -、--data - 从标准输入读取；输出文件不会覆盖已有文件。
使用 nmodel help --ai 获取机器可读说明。
`
	}
	_, err := io.WriteString(a.out, text)
	return err
}
