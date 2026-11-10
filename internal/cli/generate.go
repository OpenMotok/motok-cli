package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, failure("input_invalid", "input is unreadable or exceeds its size limit")
	}
	return b, nil
}
func (a *app) prompt() (string, error) {
	p := a.opts.prompt
	if p == "-" {
		b, err := readBounded(a.in, 1<<20)
		if err != nil {
			return "", err
		}
		p = string(b)
	}
	if strings.TrimSpace(p) == "" {
		return "", failure("prompt_missing", "provide --prompt TEXT or --prompt -")
	}
	return p, nil
}
func imageFile(path string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", failure("file_read", "cannot open reference image")
	}
	defer f.Close()
	b, err := readBounded(f, 20<<20)
	if err != nil {
		return nil, "", err
	}
	typ := http.DetectContentType(b)
	switch typ {
	case "image/png", "image/jpeg", "image/webp":
		return b, typ, nil
	}
	return nil, "", failure("image_type", "reference image must be PNG, JPEG or WebP")
}
func (a *app) generate(ctx context.Context, command string) error {
	if a.opts.data != "" {
		return failure("option_invalid", "--data is supported by request; use --prompt for generation")
	}
	if a.opts.model == "" {
		return failure("model_missing", "set model in the configuration or pass --model")
	}
	prompt, err := a.prompt()
	if err != nil {
		return err
	}
	if command == "image edit" && a.opts.file == "" {
		return failure("file_missing", "image edit requires --file")
	}
	if a.opts.file != "" && command != "image edit" && command != "video generate" {
		return failure("option_invalid", "--file is supported for image edit and video generate")
	}
	if strings.HasPrefix(command, "image ") && a.connection.Protocol == "anthropic" {
		return failure("capability_unsupported", "Anthropic does not provide an image generation endpoint")
	}
	if command == "response generate" && a.connection.Protocol != "openai" {
		return failure("capability_unsupported", "Responses requires an OpenAI-compatible connection")
	}
	if command == "video generate" {
		if err = a.videoSupported(); err != nil {
			return err
		}
	}
	if a.opts.output != "" {
		if _, err = os.Lstat(a.opts.output); err == nil {
			return failure("output_exists", "output file already exists")
		}
		if !os.IsNotExist(err) {
			return failure("output_write", "cannot inspect output path")
		}
	}
	var body any
	path := ""
	switch command {
	case "text generate":
		switch a.connection.Protocol {
		case "anthropic":
			path = "/messages"
			body = map[string]any{"model": a.opts.model, "max_tokens": a.opts.maxTokens, "messages": []any{map[string]string{"role": "user", "content": prompt}}}
		case "gemini":
			model, err := escapeModel(a.opts.model)
			if err != nil {
				return err
			}
			path = "/models/" + model + ":generateContent"
			body = map[string]any{"contents": []any{map[string]any{"parts": []any{map[string]string{"text": prompt}}}}, "generationConfig": map[string]any{"maxOutputTokens": a.opts.maxTokens}}
		default:
			path = "/chat/completions"
			tokenField := a.connection.MaxTokensField
			if tokenField == "" {
				tokenField = "max_completion_tokens"
			}
			body = map[string]any{"model": a.opts.model, tokenField: a.opts.maxTokens, "messages": []any{map[string]string{"role": "user", "content": prompt}}}
		}
	case "response generate":
		path = "/responses"
		body = map[string]any{"model": a.opts.model, "input": prompt, "max_output_tokens": a.opts.maxTokens}
	case "image generate", "image edit":
		if a.opts.output == "" && !a.opts.json && !a.opts.dryRun {
			return failure("output_missing", "image generation requires --output FILE or --json")
		}
		if a.connection.Protocol == "gemini" {
			model, err := escapeModel(a.opts.model)
			if err != nil {
				return err
			}
			path = "/models/" + model + ":generateContent"
			parts := []any{map[string]string{"text": prompt}}
			if a.opts.file != "" {
				b, typ, err := imageFile(a.opts.file)
				if err != nil {
					return err
				}
				parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": typ, "data": base64.StdEncoding.EncodeToString(b)}})
			}
			if a.opts.size != "" {
				return failure("option_invalid", "--size is for OpenAI-compatible image APIs; use request for Gemini imageConfig")
			}
			body = map[string]any{"contents": []any{map[string]any{"parts": parts}}, "generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}}
		} else {
			path = "/images/generations"
			if command == "image edit" {
				path = "/images/edits"
			}
			payload := map[string]any{"model": a.opts.model, "prompt": prompt, "n": 1}
			if a.opts.size != "" {
				payload["size"] = a.opts.size
			}
			body = payload
		}
	case "video generate":
		path = "/videos"
		payload := map[string]any{"model": a.opts.model, "prompt": prompt}
		if a.opts.size != "" {
			payload["size"] = a.opts.size
		}
		if a.opts.seconds != "" {
			payload["seconds"] = a.opts.seconds
		}
		body = payload
	}
	data, err := json.Marshal(body)
	if err != nil {
		return failure("input_invalid", "cannot encode request")
	}
	contentType := "application/json"
	if a.opts.dryRun {
		// A preview reports file metadata, never embeds reference image bytes.
		if a.connection.Protocol == "gemini" && a.opts.file != "" {
			data, _ = json.Marshal(map[string]any{"model": a.opts.model, "prompt": prompt, "reference_file": a.opts.file})
		}
		if a.connection.Protocol == "openai" && (command == "image edit" || command == "video generate" && a.opts.file != "") {
			contentType = "multipart/form-data"
		}
		return a.preview("POST", path, contentType, data)
	}
	if a.connection.Protocol == "openai" && (command == "image edit" || command == "video generate" && a.opts.file != "") {
		fields := map[string]any{}
		_ = json.Unmarshal(data, &fields)
		var buffer bytes.Buffer
		w := multipart.NewWriter(&buffer)
		for key, value := range fields {
			encoded, _ := json.Marshal(value)
			text := string(encoded)
			if s, ok := value.(string); ok {
				text = s
			}
			if err = w.WriteField(key, text); err != nil {
				return failure("input_invalid", "cannot encode multipart field")
			}
		}
		b, _, err := imageFile(a.opts.file)
		if err != nil {
			return err
		}
		field := "image"
		if command == "video generate" {
			field = "input_reference"
		}
		part, err := w.CreateFormFile(field, filepath.Base(a.opts.file))
		if err != nil {
			return failure("input_invalid", "cannot encode reference image")
		}
		if _, err = part.Write(b); err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
		data = buffer.Bytes()
		contentType = w.FormDataContentType()
	}
	response, err := a.jsonRequest(ctx, "POST", path, contentType, data)
	if err != nil {
		return err
	}
	if strings.HasPrefix(command, "image ") {
		if a.opts.output != "" {
			if err = a.saveImage(ctx, response, a.opts.output); err != nil {
				return err
			}
			return a.print(map[string]string{"output": a.opts.output}, a.opts.output)
		}
		return a.print(response, "")
	}
	if command == "video generate" {
		var task struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(response, &task) != nil || !validID(task.ID) {
			return failure("response_invalid", "video response is missing a valid task ID")
		}
		if a.opts.wait || a.opts.output != "" {
			if !a.opts.json {
				_, _ = io.WriteString(a.errOut, a.localized("Waiting for video task: ", "等待视频任务：")+task.ID+"\n")
			}
			response, err = a.waitVideo(ctx, task.ID, response)
			if err != nil {
				return err
			}
			if a.opts.output != "" {
				return a.video(ctx, "download", task.ID)
			}
		}
		return a.print(response, "")
	}
	if a.opts.json && a.opts.output == "" {
		return a.print(response, "")
	}
	text := extractText(response)
	if text == "" {
		return failure("text_missing", "response contains no text; use --json to inspect the native response")
	}
	if a.opts.output != "" {
		if err = writeNew(a.opts.output, []byte(text)); err != nil {
			return err
		}
		return a.print(map[string]string{"output": a.opts.output}, a.opts.output)
	}
	return a.print(nil, text)
}
func extractText(data []byte) string {
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &r) != nil {
		return ""
	}
	if len(r.Choices) > 0 {
		return r.Choices[0].Message.Content
	}
	var text strings.Builder
	for _, part := range r.Content {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	for _, item := range r.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" {
				text.WriteString(part.Text)
			}
		}
	}
	if len(r.Candidates) > 0 {
		for _, part := range r.Candidates[0].Content.Parts {
			if !part.Thought {
				text.WriteString(part.Text)
			}
		}
	}
	return text.String()
}

var taskID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

func validID(id string) bool { return taskID.MatchString(id) }
func (a *app) videoSupported() error {
	if a.connection.Protocol != "openai" {
		return failure("capability_unsupported", "video commands require a service implementing the OpenAI-compatible Videos contract")
	}
	u, _ := url.Parse(a.connection.BaseURL)
	if strings.EqualFold(u.Hostname(), "api.openai.com") {
		return failure("capability_unavailable", "the official OpenAI Videos API is unavailable; configure a video-capable compatible service")
	}
	return nil
}
func (a *app) video(ctx context.Context, action, id string) error {
	if err := a.videoSupported(); err != nil {
		return err
	}
	if !validID(id) {
		return failure("id_invalid", "invalid task ID")
	}
	path := "/videos/" + id
	if action == "download" {
		if a.opts.output == "" {
			return failure("output_missing", "video download requires --output")
		}
		path += "/content"
	}
	if a.opts.dryRun {
		return a.preview("GET", path, "", nil)
	}
	if action == "download" {
		resp, err := a.do(ctx, "GET", path, "", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err = saveStream(a.opts.output, resp.Body); err != nil {
			return err
		}
		return a.print(map[string]string{"id": id, "output": a.opts.output}, a.opts.output)
	}
	data, err := a.jsonRequest(ctx, "GET", path, "", nil)
	if err != nil {
		return err
	}
	return a.print(data, "")
}
func (a *app) waitVideo(ctx context.Context, id string, data json.RawMessage) (json.RawMessage, error) {
	for {
		var task struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &task) != nil {
			return nil, failure("response_invalid", "invalid video status response")
		}
		switch task.Status {
		case "completed", "succeeded":
			return data, nil
		case "failed", "cancelled", "canceled", "expired":
			return nil, failure("video_failed", "video task did not complete: "+id)
		case "queued", "pending", "running", "in_progress":
		default:
			return nil, failure("response_invalid", "unrecognized video task status: "+id)
		}
		timer := time.NewTimer(a.opts.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, failure("video_timeout", "video polling interrupted; resume using video status "+id)
		case <-timer.C:
		}
		var err error
		data, err = a.jsonRequest(ctx, "GET", "/videos/"+id, "", nil)
		if err != nil {
			return nil, err
		}
	}
}
func (a *app) raw(ctx context.Context, method, path string) error {
	method = strings.ToUpper(method)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return failure("method_invalid", "unsupported HTTP method")
	}
	u, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return failure("path_invalid", "request endpoint must be a relative API path starting with /")
	}
	var data []byte
	if a.opts.data != "" {
		if a.opts.data == "-" {
			data, err = readBounded(a.in, 4<<20)
		} else {
			var f *os.File
			f, err = os.Open(a.opts.data)
			if err == nil {
				defer f.Close()
				data, err = readBounded(f, 4<<20)
			}
		}
		if err != nil {
			return failure("input_invalid", "cannot read JSON request data")
		}
		if !json.Valid(data) {
			return failure("input_invalid", "request data is not valid JSON")
		}
	}
	contentType := ""
	if len(data) > 0 {
		contentType = "application/json"
	}
	if a.opts.dryRun {
		return a.preview(method, path, contentType, data)
	}
	if a.opts.output != "" {
		resp, err := a.do(ctx, method, path, contentType, data)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err = saveStream(a.opts.output, resp.Body); err != nil {
			return err
		}
		return a.print(map[string]string{"output": a.opts.output}, a.opts.output)
	}
	response, err := a.jsonRequest(ctx, method, path, contentType, data)
	if err != nil {
		return err
	}
	return a.print(response, "")
}
