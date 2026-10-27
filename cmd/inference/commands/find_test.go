package commands

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/inference/internal/snapcatalog"
	"github.com/canonical/inference/internal/snapd"
	"github.com/mattn/go-runewidth"
)

func TestFindFiltersStoreResults(t *testing.T) {
	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[
		{"snap":"qwen3"},
		{"snap":"aaa-related"},
		{"snap":"gemma4","engines":[{"name":"nvidia-gpu"},{"name":"cpu"}],
		 "models":[{"name":"small","capabilities":["text"]},{"name":"large","capabilities":["vision","text"]}]}
	]`)
	socket := filepath.Join(t.TempDir(), "snapd.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/find" || request.URL.Query().Get("q") != "Gemma 4" {
			t.Errorf("unexpected request %s", request.URL)
		}
		fmt.Fprint(w, `{"type":"sync","result":[{"name":"box-linux"},{"name":"aaa-related","title":"Gemma 4"},{"name":"gemma4"},{"name":"gemma4"}]}`)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	ctx.SnapdClient = &snapd.Client{Socket: socket}

	if err := execute(Find(ctx), "Gemma 4"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%-13s%-55s%s\n", "NAME", "OPTIMIZATIONS", "CAPABILITIES") +
		fmt.Sprintf("%-13s%-55s%s\n", "gemma4", "cpu, nvidia-gpu", "text, vision") +
		fmt.Sprintf("%-13s%-55s%s\n", "aaa-related", "-", "-")
	if got := stdout.String(); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFindRanking(t *testing.T) {
	for _, test := range []struct {
		name    string
		query   string
		results []snapd.FindResult
		want    string
	}{
		{
			name: "name before description", query: "qwen",
			results: []snapd.FindResult{{Name: "mimo-v2-6", Description: "Distilled from Qwen"}, {Name: "qwen3"}},
			want:    "qwen3,mimo-v2-6",
		},
		{
			name: "field priority", query: " GeMmA ",
			results: []snapd.FindResult{
				{Name: "unmatched"},
				{Name: "description", Description: "Gemma model"},
				{Name: "summary", Summary: "Gemma model"},
				{Name: "title", Title: "Gemma model"},
				{Name: "gemma4"},
			},
			want: "gemma4,title,summary,description,unmatched",
		},
		{
			name: "more matching words first", query: "gemma 4",
			results: []snapd.FindResult{{Name: "gemma3"}, {Name: "gemma4"}},
			want:    "gemma4,gemma3",
		},
		{
			name: "lower fields break ties", query: "qwen",
			results: []snapd.FindResult{{Name: "qwen3"}, {Name: "qwen-vl", Title: "Qwen vision"}},
			want:    "qwen-vl,qwen3",
		},
		{
			name: "ties preserve store order", query: "qwen",
			results: []snapd.FindResult{{Name: "qwen3-8"}, {Name: "qwen3-5"}},
			want:    "qwen3-8,qwen3-5",
		},
		{
			name: "repeated query words count once", query: "gemma GEMMA 4",
			results: []snapd.FindResult{{Name: "4", Title: "Gemma"}, {Name: "gemma"}},
			want:    "4,gemma",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rankFindResults(test.results, test.query)
			var names []string
			for _, result := range test.results {
				names = append(names, result.Name)
			}
			if got := strings.Join(names, ","); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestFindTableMissingMetadata(t *testing.T) {
	for _, models := range [][]snapcatalog.Model{nil, {{Name: "embedding", Capabilities: nil}}} {
		cmd := &findCommand{}
		got, err := cmd.outputTable([]snapcatalog.Entry{{SnapName: "gemma4", Models: models}})
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%-8s%-60s%s\n", "NAME", "OPTIMIZATIONS", "CAPABILITIES") +
			fmt.Sprintf("%-8s%-60s%s\n", "gemma4", "-", "-")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestFindTableWidth(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry snapcatalog.Entry
	}{
		{
			name: "long optimizations",
			entry: snapcatalog.Entry{
				SnapName: "nemotron-3-5-bolt",
				Engines: []snapcatalog.Engine{
					{Name: "amd-gpu"}, {Name: "cpu"}, {Name: "intel-cpu"},
					{Name: "intel-gpu"}, {Name: "intel-onemkl"}, {Name: "nvidia-gpu"},
				},
				Models: []snapcatalog.Model{{Capabilities: []string{"text", "vision", "thinking", "tools"}}},
			},
		},
		{
			name: "long capabilities and name",
			entry: snapcatalog.Entry{
				SnapName: strings.Repeat("long-name-", 8),
				Engines:  []snapcatalog.Engine{{Name: "nvidia-gpu"}},
				Models:   []snapcatalog.Model{{Capabilities: []string{strings.Repeat("capability", 12)}}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := (&findCommand{}).outputTable([]snapcatalog.Entry{test.entry})
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("expected header and one truncated row, got %q", got)
			}
			for _, line := range lines {
				if width := runewidth.StringWidth(line); width > 80 {
					t.Fatalf("line width %d exceeds 80: %q", width, line)
				}
			}
		})
	}
}

func TestFindTableNoMatches(t *testing.T) {
	got, err := (&findCommand{}).outputTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != fmt.Sprintf("%-6s%-62s%s\n", "NAME", "OPTIMIZATIONS", "CAPABILITIES") {
		t.Fatalf("unexpected empty output %q", got)
	}
}

func TestFindInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"gemma", "4"}, {""}, {"   "}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			ctx, stdout, _ := newTestContext()
			if err := execute(Find(ctx), args...); err == nil {
				t.Fatal("expected argument error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected output %q", stdout.String())
			}
		})
	}
}

func TestFindCatalogError(t *testing.T) {
	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `{`)
	err := execute(Find(ctx), "gemma")
	if err == nil || !strings.Contains(err.Error(), "decoding published snap catalog") {
		t.Fatalf("unexpected error %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output %q", stdout.String())
	}
}

func TestFindSnapdError(t *testing.T) {
	ctx, stdout, _ := newTestContext()
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[]`)
	ctx.SnapdClient = &snapd.Client{Socket: filepath.Join(t.TempDir(), "missing.socket")}
	if err := execute(Find(ctx), "gemma"); err == nil {
		t.Fatal("expected snapd error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output %q", stdout.String())
	}
}
