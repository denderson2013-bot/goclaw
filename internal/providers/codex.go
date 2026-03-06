package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CodexProvider implements Provider for the OpenAI Responses API,
// used with ChatGPT subscription via OAuth (Codex flow).
// Wire format: POST /codex/responses on chatgpt.com backend.
type CodexProvider struct {
	name         string
	apiBase      string // e.g. "https://api.openai.com/v1" or "https://chatgpt.com/backend-api"
	defaultModel string
	client       *http.Client
	retryConfig  RetryConfig
	tokenSource  TokenSource
}

// NewCodexProvider creates a provider for the OpenAI Responses API with OAuth token.
func NewCodexProvider(name string, tokenSource TokenSource, apiBase, defaultModel string) *CodexProvider {
	if apiBase == "" {
		apiBase = "https://chatgpt.com/backend-api"
	}
	apiBase = strings.TrimRight(apiBase, "/")

	if defaultModel == "" {
		defaultModel = "gpt-5.3-codex"
	}

	return &CodexProvider{
		name:         name,
		apiBase:      apiBase,
		defaultModel: defaultModel,
		client:       &http.Client{Timeout: 300 * time.Second},
		retryConfig:  DefaultRetryConfig(),
		tokenSource:  tokenSource,
	}
}

func (p *CodexProvider) Name() string         { return p.name }
func (p *CodexProvider) DefaultModel() string  { return p.defaultModel }
func (p *CodexProvider) SupportsThinking() bool { return true }

func (p *CodexProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Codex Responses API requires stream=true; delegate to ChatStream with no chunk handler.
	return p.ChatStream(ctx, req, nil)
}

func (p *CodexProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	body := p.buildRequestBody(req, true)

	respBody, err := RetryDo(ctx, p.retryConfig, func() (io.ReadCloser, error) {
		return p.doRequest(ctx, body)
	})
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	result := &ChatResponse{FinishReason: "stop"}
	toolCalls := make(map[string]*responsesToolCallAcc) // keyed by item_id

	scanner := bufio.NewScanner(respBody)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")
		if data == "[DONE]" {
			break
		}

		var event responsesSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				result.Content += event.Delta
				if onChunk != nil {
					onChunk(StreamChunk{Content: event.Delta})
				}
			}

		case "response.function_call_arguments.delta":
			if event.ItemID != "" {
				acc := toolCalls[event.ItemID]
				if acc == nil {
					acc = &responsesToolCallAcc{}
					toolCalls[event.ItemID] = acc
				}
				acc.rawArgs += event.Delta
			}

		case "response.output_item.done":
			if event.Item != nil {
				switch event.Item.Type {
				case "function_call":
					acc := toolCalls[event.Item.ID]
					if acc == nil {
						acc = &responsesToolCallAcc{}
					}
					acc.callID = event.Item.CallID
					acc.name = event.Item.Name
					if event.Item.Arguments != "" {
						acc.rawArgs = event.Item.Arguments
					}
					toolCalls[event.Item.ID] = acc

				case "reasoning":
					if len(event.Item.Summary) > 0 {
						for _, s := range event.Item.Summary {
							if s.Text != "" {
								result.Thinking += s.Text
								if onChunk != nil {
									onChunk(StreamChunk{Thinking: s.Text})
								}
							}
						}
					}
				}
			}

		case "response.completed":
			if event.Response != nil && event.Response.Usage != nil {
				u := event.Response.Usage
				result.Usage = &Usage{
					PromptTokens:     u.InputTokens,
					CompletionTokens: u.OutputTokens,
					TotalTokens:      u.TotalTokens,
				}
				if u.OutputTokensDetails != nil {
					result.Usage.ThinkingTokens = u.OutputTokensDetails.ReasoningTokens
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: stream read error: %w", p.name, err)
	}

	// Build tool calls from accumulators
	for _, acc := range toolCalls {
		if acc.name == "" {
			continue
		}
		args := make(map[string]interface{})
		_ = json.Unmarshal([]byte(acc.rawArgs), &args)
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        acc.callID,
			Name:      acc.name,
			Arguments: args,
		})
	}

	if len(result.ToolCalls) > 0 {
		result.FinishReason = "tool_calls"
	}

	if onChunk != nil {
		onChunk(StreamChunk{Done: true})
	}

	return result, nil
}

// buildRequestBody converts internal ChatRequest to Responses API format.
func (p *CodexProvider) buildRequestBody(req ChatRequest, stream bool) map[string]interface{} {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	// Separate system messages as instructions, convert rest to input items
	var instructions string
	var input []interface{}

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if instructions == "" {
				instructions = m.Content
			} else {
				instructions += "\n\n" + m.Content
			}

		case "user":
			if len(m.Images) > 0 {
				// Multimodal input
				var parts []map[string]interface{}
				for _, img := range m.Images {
					parts = append(parts, map[string]interface{}{
						"type":      "input_image",
						"image_url": fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Data),
					})
				}
				if m.Content != "" {
					parts = append(parts, map[string]interface{}{
						"type": "input_text",
						"text": m.Content,
					})
				}
				input = append(input, map[string]interface{}{
					"role":    "user",
					"content": parts,
				})
			} else {
				input = append(input, map[string]interface{}{
					"role":    "user",
					"content": m.Content,
				})
			}

		case "assistant":
			// Assistant messages with tool calls → separate function_call items
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Arguments)
					// Responses API requires "id" to start with "fc_"; call_id is the tool call ID.
					itemID := tc.ID
					if !strings.HasPrefix(itemID, "fc") {
						itemID = "fc_" + tc.ID
					}
					input = append(input, map[string]interface{}{
						"type":      "function_call",
						"id":        itemID,
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": string(argsJSON),
					})
				}
			}
			// Also include text message if present
			if m.Content != "" {
				input = append(input, map[string]interface{}{
					"type": "message",
					"role": "assistant",
					"content": []map[string]interface{}{
						{"type": "output_text", "text": m.Content},
					},
				})
			}

		case "tool":
			// Tool results → function_call_output items
			output := m.Content
			input = append(input, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  output,
			})
		}
	}

	body := map[string]interface{}{
		"model":  model,
		"input":  input,
		"stream": stream,
		"store":  false,
	}

	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	body["instructions"] = instructions

	// Convert tools to Responses API format (internally tagged)
	if len(req.Tools) > 0 {
		var tools []map[string]interface{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			})
		}
		body["tools"] = tools
	}

	// Options — chatgpt.com backend does not support max_output_tokens or temperature

	if level, ok := req.Options[OptThinkingLevel].(string); ok && level != "" && level != "off" {
		body["reasoning"] = map[string]interface{}{"effort": level}
	}

	return body
}

func (p *CodexProvider) doRequest(ctx context.Context, body interface{}) (io.ReadCloser, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", p.name, err)
	}

	endpoint := p.apiBase + "/codex/responses"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", p.name, err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	token, err := p.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("%s: get auth token: %w", p.name, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("OpenAI-Beta", "responses=v1")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", p.name, err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		retryAfter := ParseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, &HTTPError{
			Status:     resp.StatusCode,
			Body:       fmt.Sprintf("%s: %s", p.name, string(respBody)),
			RetryAfter: retryAfter,
		}
	}

	return resp.Body, nil
}

func (p *CodexProvider) parseResponse(resp *responsesAPIResponse) *ChatResponse {
	result := &ChatResponse{FinishReason: "stop"}

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					result.Content += c.Text
				}
			}

		case "function_call":
			args := make(map[string]interface{})
			_ = json.Unmarshal([]byte(item.Arguments), &args)
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: args,
			})

		case "reasoning":
			for _, s := range item.Summary {
				if s.Text != "" {
					result.Thinking += s.Text
				}
			}
		}
	}

	if len(result.ToolCalls) > 0 {
		result.FinishReason = "tool_calls"
	}

	if resp.Usage != nil {
		result.Usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
		if resp.Usage.OutputTokensDetails != nil {
			result.Usage.ThinkingTokens = resp.Usage.OutputTokensDetails.ReasoningTokens
		}
	}

	return result
}

// --- Wire types for the Responses API ---

type responsesAPIResponse struct {
	ID     string              `json:"id"`
	Object string              `json:"object"`
	Model  string              `json:"model"`
	Output []responsesItem     `json:"output"`
	Usage  *responsesUsage     `json:"usage,omitempty"`
	Status string              `json:"status"`
}

type responsesItem struct {
	ID        string               `json:"id"`
	Type      string               `json:"type"` // "message", "function_call", "reasoning"
	Role      string               `json:"role,omitempty"`
	Content   []responsesContent   `json:"content,omitempty"`
	CallID    string               `json:"call_id,omitempty"`
	Name      string               `json:"name,omitempty"`
	Arguments string               `json:"arguments,omitempty"`
	Summary   []responsesSummary   `json:"summary,omitempty"`
}

type responsesContent struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

type responsesSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens         int                     `json:"input_tokens"`
	OutputTokens        int                     `json:"output_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	OutputTokensDetails *responsesTokensDetails `json:"output_tokens_details,omitempty"`
}

type responsesTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// SSE streaming types

type responsesSSEEvent struct {
	Type     string               `json:"type"`
	Delta    string               `json:"delta,omitempty"`
	ItemID   string               `json:"item_id,omitempty"`
	Item     *responsesItem       `json:"item,omitempty"`
	Response *responsesAPIResponse `json:"response,omitempty"`
}

type responsesToolCallAcc struct {
	callID  string
	name    string
	rawArgs string
}
