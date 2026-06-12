// Package chat provides the one-shot `run` command and the interactive REPL.
// Both speak OpenAI chat-completions, preferring the local proxy when it is up
// and otherwise routing directly to a discovered backend.
package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"inference/internal/backend"
	"inference/internal/config"
	"inference/internal/proxy"
	"inference/internal/ui"
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// endpoint is a resolved OpenAI-compatible target.
type endpoint struct {
	baseURL string // without trailing slash
	model   string
	apiKey  string
	via     string // "proxy" | backend name
}

// resolve picks where to send requests for the given model.
func resolve(cfg *config.Config, model string) (*endpoint, error) {
	if proxy.IsUp(cfg) {
		base := fmt.Sprintf("http://%s:%d/v1", cfg.Proxy.Bind, cfg.Proxy.Port)
		if model == "" {
			if m := firstProxyModel(base); m != "" {
				model = m
			}
		}
		if model == "" {
			return nil, fmt.Errorf("no model available; install one with `inference install gemma4`")
		}
		return &endpoint{baseURL: base, model: model, via: "proxy"}, nil
	}

	// Proxy down: route directly to a discovered backend.
	bs := backend.Discover(cfg)
	var b *backend.Backend
	if model == "" {
		b, model = backend.Default(bs)
	} else {
		b, model = backend.FindForModel(bs, cfg.Aliases, model)
	}
	if b == nil {
		return nil, fmt.Errorf("no backend serves model %q (is an inference snap installed and running?)", model)
	}
	return &endpoint{baseURL: b.BaseURL, model: model, apiKey: b.APIKey, via: b.Name}, nil
}

func firstProxyModel(base string) string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/models")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var doc struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&doc)
	for _, m := range doc.Data {
		if m.OwnedBy != "alias" {
			return m.ID
		}
	}
	return ""
}

// Run performs a single completion and prints the result.
func Run(cfg *config.Config, model, prompt string) error {
	if prompt == "" {
		// Read prompt from stdin (pipe support).
		b, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		return fmt.Errorf("no prompt given (pass an argument or pipe via stdin)")
	}
	ep, err := resolve(cfg, model)
	if err != nil {
		return err
	}
	msgs := []message{{Role: "user", Content: prompt}}
	raw, content, err := complete(ep, msgs, false)
	if err != nil {
		return err
	}
	if ui.JSON {
		fmt.Println(string(raw))
		return nil
	}
	fmt.Println(content)
	return nil
}

// REPL runs an interactive chat session.
func REPL(cfg *config.Config, model string) error {
	ep, err := resolve(cfg, model)
	if err != nil {
		return err
	}
	ui.Printf("%s · %s · /help for commands\n",
		ui.Bold(ep.model), ui.Dim("via "+ep.via))

	var msgs []message
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1024*1024), 1024*1024)
	for {
		fmt.Print(ui.Cyan("> "))
		if !in.Scan() {
			fmt.Println()
			return nil
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			done, err := slash(cfg, ep, &msgs, line)
			if err != nil {
				ui.Errf("%s %v\n", ui.Red(ui.SymErr), err)
			}
			if done {
				return nil
			}
			continue
		}
		msgs = append(msgs, message{Role: "user", Content: line})
		_, content, err := complete(ep, msgs, true)
		if err != nil {
			ui.Errf("%s %v\n", ui.Red(ui.SymErr), err)
			msgs = msgs[:len(msgs)-1] // drop the failed turn
			continue
		}
		msgs = append(msgs, message{Role: "assistant", Content: content})
	}
}

// slash handles REPL slash-commands. Returns done=true to exit.
func slash(cfg *config.Config, ep *endpoint, msgs *[]message, line string) (bool, error) {
	cmd, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "/quit", "/exit", "/q":
		return true, nil
	case "/help":
		ui.Println(ui.Dim("/model <id>  /system <text>  /models  /clear  /quit"))
	case "/clear":
		*msgs = nil
		ui.Println(ui.Dim("(history cleared)"))
	case "/system":
		// Replace or set the leading system message.
		ng := []message{{Role: "system", Content: arg}}
		for _, m := range *msgs {
			if m.Role != "system" {
				ng = append(ng, m)
			}
		}
		*msgs = ng
		ui.Println(ui.Dim("(system prompt set)"))
	case "/model":
		if arg == "" {
			return false, fmt.Errorf("usage: /model <id>")
		}
		nep, err := resolve(cfg, arg)
		if err != nil {
			return false, err
		}
		*ep = *nep
		ui.Printf("%s now using %s (%s)\n", ui.Green(ui.SymOK), ui.Bold(ep.model), ep.via)
	case "/models":
		t := ui.NewTable()
		for _, b := range backend.Discover(cfg) {
			t.Row(b.Name, ui.Dim(strings.Join(b.Models, ", ")))
		}
		t.Render()
	default:
		return false, fmt.Errorf("unknown command %q (try /help)", cmd)
	}
	return false, nil
}

// complete posts a chat completion. When stream is true it prints deltas as
// they arrive and returns the assembled content; otherwise it returns the full
// response body and the message content.
func complete(ep *endpoint, msgs []message, stream bool) (raw []byte, content string, err error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model":    ep.model,
		"messages": msgs,
		"stream":   stream,
	})
	req, err := http.NewRequest(http.MethodPost, ep.baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.apiKey)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	if !stream {
		raw, _ = io.ReadAll(resp.Body)
		return raw, extractContent(raw), nil
	}

	// Streaming: parse SSE "data: {delta}" lines.
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta.Content
		if d != "" {
			fmt.Print(d)
			sb.WriteString(d)
		}
	}
	fmt.Println()
	return nil, sb.String(), nil
}

func extractContent(raw []byte) string {
	var doc struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &doc) == nil && len(doc.Choices) > 0 {
		return doc.Choices[0].Message.Content
	}
	return string(raw)
}
