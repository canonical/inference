package openaiproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/canonical/inference/internal/providers"
)

func TestProxyHandlerPreservesSnapshotWhenRefreshFails(t *testing.T) {
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"ready"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"id":"completion"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler := NewProxyHandler(HandlerOptions{
		ListProviders: func(context.Context) ([]providers.Provider, error) {
			if fail.Load() {
				return nil, errors.New("provider inventory unavailable")
			}
			return []providers.Provider{{Name: "provider", BaseURL: upstream.URL + "/v1"}}, nil
		},
		Client: upstream.Client(),
		Logger: discardLogger(),
	})
	if err := handler.RefreshRouting(context.Background()); err != nil {
		t.Fatal(err)
	}

	fail.Store(true)
	if err := handler.RefreshRouting(context.Background()); err == nil {
		t.Fatal("refresh succeeded, want error")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"provider/ready"}`)),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d body %s, want 200", response.Code, response.Body.String())
	}
}
