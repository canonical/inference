package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/canonical/inference/cmd/inference/common"
	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/canonical/inference/internal/storage"
)

type disabledFixture struct {
	ctx           *common.Context
	stdout        func() string
	gemmaRequests *atomic.Int32
	qwenRequests  *atomic.Int32
}

func newDisabledFixture(t *testing.T) disabledFixture {
	t.Helper()
	upstream := func(model string, requests *atomic.Int32) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": model}}})
		}))
		t.Cleanup(server.Close)
		return server.URL + "/v1"
	}
	var gemmaRequests, qwenRequests atomic.Int32
	gemmaURL := upstream("gemma-model", &gemmaRequests)
	qwenURL := upstream("qwen-model", &qwenRequests)

	root := t.TempDir()
	for name, baseURL := range map[string]string{"gemma4": gemmaURL, "qwen3": qwenURL} {
		dir := filepath.Join(root, name+"-mount")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		env := "OPENAI_BASE_URL=" + baseURL + "\nSNAP_NAME=" + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, "provider.env"), []byte(env), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"gemma4","model_name":"Gemma 4","repo_url":"https://example.com/gemma4"},
		{"snap":"qwen3","model_name":"Qwen 3","repo_url":"https://example.com/qwen3"}
	]`)
	ctx.SnapdClient, _ = snapd.NewFakeServer(t, map[string]string{
		"gemma4": snapd.SnapStatusActive,
		"qwen3":  snapd.SnapStatusActive,
	})
	ctx.ShareProvidersPath = root
	ctx.Storage = &storage.Storage{Path: filepath.Join(t.TempDir(), "storage.json")}
	if _, err := ctx.Storage.SetProviderEnabled("qwen3", false); err != nil {
		t.Fatal(err)
	}

	return disabledFixture{ctx: ctx, stdout: stdout.String, gemmaRequests: &gemmaRequests, qwenRequests: &qwenRequests}
}

func TestStatusOmitsDisabledProviders(t *testing.T) {
	f := newDisabledFixture(t)
	cmd := &statusCommand{Context: f.ctx}

	health, err := cmd.providerHealth(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if want := map[string]string{"gemma4": "ok"}; !reflect.DeepEqual(health, want) {
		t.Fatalf("health = %v, want %v", health, want)
	}
	if f.qwenRequests.Load() != 0 {
		t.Fatalf("disabled provider was queried %d times", f.qwenRequests.Load())
	}
}

func TestModelsOmitsDisabledProviders(t *testing.T) {
	f := newDisabledFixture(t)

	if err := execute(Models(f.ctx), "--format=json"); err != nil {
		t.Fatal(err)
	}

	var got modelsOutput
	if err := json.Unmarshal([]byte(f.stdout()), &got); err != nil {
		t.Fatal(err)
	}
	want := []modelOutput{{ID: "gemma4/gemma-model", Provider: "gemma4", Model: "gemma-model"}}
	if !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("models = %+v, want %+v", got.Models, want)
	}
	if f.qwenRequests.Load() != 0 {
		t.Fatalf("disabled provider was queried %d times", f.qwenRequests.Load())
	}
}
