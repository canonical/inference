package openaiproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/canonical/inference/internal/config"
	"github.com/canonical/inference/internal/providers"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
)

func TestProxyFollowsProviderEnabledSettingOnRefresh(t *testing.T) {
	var chatRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			chatRequests.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "model"}}})
	}))
	defer upstream.Close()

	root := t.TempDir()
	writeProvider(t, root, "mount", "gemma4", upstream.URL+"/v1")
	client, _ := snapd.NewFakeServer(t, map[string]string{"gemma4": snapd.SnapStatusActive})
	configReader := &config.ConfigReader{Path: filepath.Join(t.TempDir(), "config.json")}
	service := providers.NewService(snapcatalog.WriteFakeCatalog(t, `[]`), client, root, configReader)
	handler := NewModelsHandler(service, upstream.Client(), discardLogger())

	listModels := func() []string {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("got status %d: %s", response.Code, response.Body.String())
		}
		var got modelsResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(got.Data))
		for i, model := range got.Data {
			ids[i] = model.ID
		}
		return ids
	}
	chat := func() int {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gemma4/model"}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		return response.Code
	}

	if got := listModels(); len(got) != 1 || got[0] != "gemma4/model" {
		t.Fatalf("enabled provider models = %v, want [gemma4/model]", got)
	}
	if code := chat(); code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", code)
	}

	if _, err := configReader.SetProviderEnabled("gemma4", false); err != nil {
		t.Fatal(err)
	}
	if got := listModels(); len(got) != 0 {
		t.Fatalf("disabled provider models = %v, want none", got)
	}
	if code := chat(); code != http.StatusNotFound {
		t.Fatalf("chat status = %d, want 404", code)
	}
	if chatRequests.Load() != 1 {
		t.Fatalf("upstream received %d chat requests, want 1", chatRequests.Load())
	}

	if _, err := configReader.SetProviderEnabled("gemma4", true); err != nil {
		t.Fatal(err)
	}
	if got := listModels(); len(got) != 1 {
		t.Fatalf("re-enabled provider models = %v, want one", got)
	}
	if code := chat(); code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", code)
	}
}
