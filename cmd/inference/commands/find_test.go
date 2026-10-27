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
	ctx, stdout, stderr := newTestContext(t)
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
		if _, err := fmt.Fprint(w, `{"type":"sync","result":[{"name":"box-linux"},{"name":"aaa-related","title":"Gemma 4"},{"name":"gemma4"},{"name":"gemma4"}]}`); err != nil {
			t.Errorf("writing find response: %v", err)
		}
	}))
	if err := server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	ctx.SnapdClient = &snapd.Client{Socket: socket}

	want := fmt.Sprintf("%-13s%s\n", "NAME", "CAPABILITIES") +
		fmt.Sprintf("%-13s%s\n", "gemma4", "text, vision") +
		fmt.Sprintf("%-13s%s\n", "aaa-related", "-")
	for _, args := range [][]string{{"Gemma 4"}, {"Gemma", "4"}} {
		stdout.Reset()
		stderr.Reset()
		if err := execute(Find(ctx), args...); err != nil {
			t.Fatal(err)
		}
		if got := stdout.String(); got != want {
			t.Fatalf("args %q: got:\n%q\nwant:\n%q", args, got, want)
		}
		if got := stderr.String(); got != "\n"+`Hint: Run "inference info <name>" for details.`+"\n" {
			t.Fatalf("unexpected stderr %q", got)
		}
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
		want := fmt.Sprintf("%-8s%s\n", "NAME", "CAPABILITIES") +
			fmt.Sprintf("%-8s%s\n", "gemma4", "-")
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
			name: "engines omitted",
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

func TestFindNoMatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		result string
	}{
		{name: "empty store results", result: `[]`},
		{name: "no catalogue matches", result: `[{"name":"box-linux"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, stdout, stderr := newTestContext(t)
			ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[{"snap":"gemma4"}]`)
			socket := filepath.Join(t.TempDir(), "snapd.socket")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := fmt.Fprintf(w, `{"type":"sync","result":%s}`, test.result); err != nil {
					t.Errorf("writing find response: %v", err)
				}
			}))
			if err := server.Listener.Close(); err != nil {
				t.Fatal(err)
			}
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)
			ctx.SnapdClient = &snapd.Client{Socket: socket}

			if err := execute(Find(ctx), "unknown"); err != nil {
				t.Fatalf("expected successful empty search: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if got := stderr.String(); got != "No inference snaps matched the query \"unknown\"\n" {
				t.Fatalf("unexpected stderr %q", got)
			}
		})
	}
}

func TestFindListsCatalogueWithoutQuery(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {"   "}, {"", " "}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			ctx, stdout, stderr := newTestContext(t)
			ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[{"snap":"qwen3"},{"snap":"gemma4"}]`)
			if err := execute(Find(ctx), args...); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("%-8s%s\n", "NAME", "CAPABILITIES") +
				fmt.Sprintf("%-8s%s\n", "gemma4", "-") +
				fmt.Sprintf("%-8s%s\n", "qwen3", "-")
			if got := stdout.String(); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			if got := stderr.String(); got != "\n"+`Hint: Run "inference info <name>" for details.`+"\n" {
				t.Fatalf("unexpected stderr %q", got)
			}
		})
	}
}

func TestFindEmptyCatalogueWithoutQuery(t *testing.T) {
	ctx, stdout, stderr := newTestContext(t)
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[]`)
	if err := execute(Find(ctx)); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.String() != "No inference snaps matched the query \"\"\n" {
		t.Fatalf("unexpected stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestFindCatalogError(t *testing.T) {
	ctx, stdout, _ := newTestContext(t)
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
	ctx, stdout, _ := newTestContext(t)
	ctx.SnapCatalog = snapcatalog.WriteFakeCatalog(t, `[]`)
	ctx.SnapdClient = &snapd.Client{Socket: filepath.Join(t.TempDir(), "missing.socket")}
	if err := execute(Find(ctx), "gemma"); err == nil {
		t.Fatal("expected snapd error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output %q", stdout.String())
	}
}
