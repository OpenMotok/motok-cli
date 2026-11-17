package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAssetID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestAssetsUploadPurposeAndRead(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authentication")
		}
		switch r.Method {
		case "POST":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			f, h, e := r.FormFile("file")
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if h.Filename != "character.png" || !strings.HasPrefix(string(b), "\x89PNG") {
				t.Error("wrong upload")
			}
		case "PATCH":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["purpose"] != "virtual_portrait" {
				t.Error("wrong purpose")
			}
		}
		io.WriteString(w, `{"id":"`+testAssetID+`","purpose":"virtual_portrait"}`)
	}))
	defer server.Close()
	cfg := Config{Version: 1, Channels: map[string]Channel{"motok": {BaseURL: server.URL + "/custom/v1", APIKey: "test-key"}}, Routes: map[string]Route{"video": {Channel: "motok"}}}
	env := envConfig(t, cfg)
	path := filepath.Join(t.TempDir(), "character.png")
	os.WriteFile(path, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...), 0600)
	for _, args := range [][]string{{"assets", "upload", path, "--purpose", "virtual_portrait", "--json"}, {"assets", "list", "--kind", "image", "--before", testAssetID}, {"assets", "show", testAssetID}, {"assets", "update", testAssetID, "--purpose", "virtual_portrait"}} {
		if code, out, e := runTest(t, env, "", args...); code != 0 || !strings.Contains(out, testAssetID) {
			t.Fatal(code, out, e)
		}
	}
	want := []string{"POST /custom/v1/assets", "PATCH /custom/v1/assets/" + testAssetID, "GET /custom/v1/assets?before=" + testAssetID + "&kind=image", "GET /custom/v1/assets/" + testAssetID, "PATCH /custom/v1/assets/" + testAssetID}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatal(calls)
	}
	calls = nil
	if code, out, e := runTest(t, env, "", "assets", "upload", path, "--purpose", "virtual_portrait", "--dry-run"); code != 0 || len(calls) != 0 || strings.Contains(out, "test-key") {
		t.Fatal(code, out, e, calls)
	}
	for _, args := range [][]string{{"assets", "update", testAssetID, "--purpose", "real_person"}, {"assets", "show", "../secret"}, {"assets", "list", "--kind", "audio"}} {
		if code, _, _ := runTest(t, env, "", args...); code == 0 {
			t.Fatal("invalid arguments accepted")
		}
	}
}
func TestAssetsRejectOfficialConnections(t *testing.T) {
	for _, c := range []Channel{{Protocol: "openai", BaseURL: "https://api.openai.com/v1"}, {Protocol: "anthropic"}, {Protocol: "gemini"}} {
		c.APIKey = "test-key"
		env := envConfig(t, Config{Version: 1, Channels: map[string]Channel{"official": c}, Routes: map[string]Route{"video": {Channel: "official"}}})
		if code, _, e := runTest(t, env, "", "assets", "list", "--dry-run"); code == 0 || !strings.Contains(e, "assets_unsupported") {
			t.Fatal(code, e)
		}
	}
}

func TestAssetPurposeFailureRetainsUploadedID(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "POST" {
			io.WriteString(w, `{"id":"`+testAssetID+`"}`)
		} else {
			w.WriteHeader(503)
			io.WriteString(w, `{"secret":"must-not-leak"}`)
		}
	}))
	defer server.Close()
	env := envConfig(t, Config{Version: 1, Channels: map[string]Channel{"motok": {BaseURL: server.URL + "/v1", APIKey: "test-key"}}, Routes: map[string]Route{"video": {Channel: "motok"}}})
	path := filepath.Join(t.TempDir(), "image.png")
	os.WriteFile(path, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...), 0600)
	code, _, errOut := runTest(t, env, "", "assets", "upload", path, "--purpose", "virtual_portrait", "--json")
	var body map[string]map[string]any
	if code != 1 || json.Unmarshal([]byte(errOut), &body) != nil || body["error"]["asset_id"] != testAssetID || calls != 2 || strings.Contains(errOut, "must-not-leak") {
		t.Fatal(code, errOut, calls)
	}
}
