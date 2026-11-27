package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// pageCloud sends one page image to a cloud model through the OpenAI-compatible chat API.
// Gemini, OpenAI, OpenRouter, Groq and most other providers speak this same format, so switching
// model or provider only means changing LLM_BASE_URL, LLM_API_KEY and LLM_MODEL in backend/.env.
// Same prompt, same schema and same checks afterwards as the local model.
func (v *VLM) pageCloud(ctx context.Context, img []byte, prompt string) (*VLMDoc, error) {
	schema, _ := json.Marshal(vlmSchema)
	image := "data:" + http.DetectContentType(img) + ";base64," + base64.StdEncoding.EncodeToString(img)
	body, _ := json.Marshal(map[string]any{
		"model":       v.Model,
		"temperature": 0,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": prompt + "\n\nReply with one JSON object that follows this JSON schema, and nothing else:\n" + string(schema)},
				{"type": "image_url", "image_url": map[string]string{"url": image}},
			},
		}},
	})

	for attempt := 1; ; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(v.URL, "/")+"/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+v.APIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("cloud model unreachable at %s: %w", v.URL, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()

		// Free tiers allow only a few requests a minute: wait and try again.
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 6 {
			wait := 20 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				wait = time.Duration(s) * time.Second
			}
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("cloud model %s: %s", resp.Status, strings.TrimSpace(string(raw)))
		}

		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
			return nil, fmt.Errorf("cloud model response: %s", strings.TrimSpace(string(raw)))
		}
		// Some models wrap the JSON in ```json ... ``` even when asked not to.
		content := strings.TrimSpace(out.Choices[0].Message.Content)
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")

		var doc VLMDoc
		if err := json.Unmarshal([]byte(content), &doc); err != nil {
			return nil, fmt.Errorf("model did not return valid JSON: %w", err)
		}
		return &doc, nil
	}
}
