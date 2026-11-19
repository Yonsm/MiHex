// 命令模板展开（{{...}}）与命令执行入口。
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var templates map[string]any

func init() {
	RegisterModule("runner", runnerSetup)
}

func runnerSetup(sec Section) ([]Object, error) {
	templates = sec.Map()
	return nil, nil
}

// Exec 展开参数（先查 runner 模板，再展开 {{...}}）后分发执行。
func Exec(ctx context.Context, args []any) (any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		s, ok := a.(string)
		if !ok {
			out[i] = a
			continue
		}
		if tpl, ok := templates[s]; ok {
			s = Str(tpl)
		}
		expanded, err := Expand(ctx, s)
		if err != nil {
			return nil, err
		}
		out[i] = expanded
	}
	return Dispatch(ctx, out)
}

// Expand 把文本里的 {{实体 方法 参数...}} 替换为调用结果。
func Expand(ctx context.Context, text string) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(text, "{{")
		if i < 0 {
			b.WriteString(text)
			return b.String(), nil
		}
		j := strings.Index(text[i+2:], "}}")
		if j < 0 {
			b.WriteString(text)
			return b.String(), nil
		}
		b.WriteString(text[:i])
		expr := strings.TrimSpace(text[i+2 : i+2+j])
		r, err := Dispatch(ctx, splitArgs(expr))
		if err != nil {
			return "", err
		}
		if r != nil {
			fmt.Fprint(&b, r)
		}
		text = text[i+2+j+2:]
	}
}

func splitArgs(s string) []any {
	fields := strings.Fields(s)
	out := make([]any, len(fields))
	for i, f := range fields {
		out[i] = f
	}
	return out
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Fetch 发起 HTTP 请求：data 为 nil 时 GET，否则 POST JSON，返回解析后的对象。
func Fetch(ctx context.Context, url string, data any, headers map[string]string) (map[string]any, error) {
	var out map[string]any
	if err := FetchInto(ctx, url, data, headers, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FetchInto 同 Fetch，但把响应解析进调用方提供的结构，避免中间 map 的内存开销。
func FetchInto(ctx context.Context, url string, data any, headers map[string]string, out any) error {
	var body io.Reader
	method := http.MethodGet
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}
