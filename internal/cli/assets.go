package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var assetIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (a *app) assetConnection() error {
	u, _ := url.Parse(a.connection.BaseURL)
	host := strings.ToLower(u.Hostname())
	if a.connection.Protocol != "openai" {
		return failure("assets_unsupported", "asset commands require Motok platform endpoints")
	}
	for _, domain := range []string{"openai.com", "openai.azure.com", "services.ai.azure.com", "bytepluses.com", "byteplusapi.com", "volces.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return failure("assets_unsupported", "asset commands are not supported by official direct services")
		}
	}
	return nil
}
func (a *app) assets(ctx context.Context, args []string) error {
	if err := a.assetConnection(); err != nil {
		return err
	}
	if len(args) == 0 {
		return failure("command_unknown", "use assets upload, list, show or update")
	}
	if a.opts.purpose != "" && a.opts.purpose != "reference" && a.opts.purpose != "virtual_portrait" {
		return failure("asset_purpose_invalid", "purpose must be reference or virtual_portrait")
	}
	if args[0] != "list" && (a.opts.before != "" || a.opts.assetKind != "") {
		return failure("option_invalid", "--before and --kind require assets list")
	}
	switch args[0] {
	case "upload":
		if len(args) != 2 || a.opts.file != "" {
			return failure("option_invalid", "use assets upload FILE")
		}
		return a.uploadAsset(ctx, args[1])
	case "list":
		if len(args) != 1 || a.opts.purpose != "" {
			return failure("option_invalid", "use assets list [--kind image|video] [--before ID]")
		}
		if a.opts.assetKind != "" && a.opts.assetKind != "image" && a.opts.assetKind != "video" {
			return failure("option_invalid", "kind must be image or video")
		}
		if a.opts.before != "" && !assetIDPattern.MatchString(a.opts.before) {
			return failure("id_invalid", "invalid asset cursor")
		}
		q := url.Values{}
		if a.opts.assetKind != "" {
			q.Set("kind", a.opts.assetKind)
		}
		if a.opts.before != "" {
			q.Set("before", a.opts.before)
		}
		path := "/assets"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		if a.opts.dryRun {
			return a.preview("GET", path, "", nil)
		}
		data, err := a.jsonRequest(ctx, "GET", path, "", nil)
		if err != nil {
			return err
		}
		return a.print(data, "")
	case "show", "update":
		if len(args) != 2 || !assetIDPattern.MatchString(args[1]) {
			return failure("id_invalid", "provide a valid platform asset ID")
		}
		method, typ := "GET", ""
		var body []byte
		if args[0] == "update" {
			if a.opts.purpose == "" {
				return failure("asset_purpose_invalid", "assets update requires --purpose")
			}
			method = "PATCH"
			typ = "application/json"
			body, _ = json.Marshal(map[string]string{"purpose": a.opts.purpose})
		} else if a.opts.purpose != "" {
			return failure("option_invalid", "--purpose requires upload or update")
		}
		path := "/assets/" + args[1]
		if a.opts.dryRun {
			return a.preview(method, path, typ, body)
		}
		data, err := a.jsonRequest(ctx, method, path, typ, body)
		if err != nil {
			return err
		}
		return a.print(data, "")
	}
	return failure("command_unknown", "unknown assets command")
}
func (a *app) uploadAsset(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return failure("file_read", "cannot open asset file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 50<<20 {
		return failure("asset_file_invalid", "invalid asset file or size")
	}
	data, err := readBounded(f, 50<<20)
	if err != nil {
		return err
	}
	typ := http.DetectContentType(data)
	image := typ == "image/png" || typ == "image/jpeg" || typ == "image/webp"
	if !image && typ != "video/mp4" && typ != "video/quicktime" || image && len(data) > 8<<20 {
		return failure("asset_file_invalid", "unsupported asset format or size")
	}
	if !image && a.opts.purpose == "virtual_portrait" {
		return failure("asset_purpose_invalid", "virtual_portrait requires an image")
	}
	if a.opts.dryRun {
		return a.print(map[string]any{"dry_run": true, "channel": a.connection.ChannelName, "method": "POST", "url": a.connection.BaseURL + "/assets", "file": path, "bytes": len(data), "purpose": a.opts.purpose}, "")
	}
	var buffer bytes.Buffer
	w := multipart.NewWriter(&buffer)
	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return failure("input_invalid", "cannot prepare asset upload")
	}
	if _, err = part.Write(data); err != nil {
		return failure("input_invalid", "cannot prepare asset upload")
	}
	if err = w.Close(); err != nil {
		return failure("input_invalid", "cannot prepare asset upload")
	}
	result, err := a.jsonRequest(ctx, "POST", "/assets", w.FormDataContentType(), buffer.Bytes())
	if err != nil {
		return err
	}
	var asset struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(result, &asset) != nil || !assetIDPattern.MatchString(asset.ID) {
		return failure("response_invalid", "upload did not return a platform asset ID")
	}
	if a.opts.purpose != "" {
		body, _ := json.Marshal(map[string]string{"purpose": a.opts.purpose})
		result, err = a.jsonRequest(ctx, "PATCH", "/assets/"+asset.ID, "application/json", body)
		if err != nil {
			return &cliError{Code: "asset_purpose_save_failed", Message: "file uploaded but purpose was not saved; retry assets update for " + asset.ID, AssetID: asset.ID}
		}
	}
	return a.print(result, "")
}
