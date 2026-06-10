package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// OpenAI is the OpenAI-compatible chat-completions provider. Speaks the
// /v1/chat/completions shape so the same code works against OpenAI proper,
// Ollama (http://host:11434/v1), llama.cpp's server, vLLM, and any other
// project that mimics the wire format. Stdlib-only — no SDK dep — because
// the wire shape is tiny and we want to keep the single-binary promise.
//
// Status semantics: this provider only generates the assistant message.
// session_status (active vs pending_handoff) is derived from the host's
// most recent turn via detectHandoff() so the policy stays consistent
// across providers and survives model swaps. If the upstream HTTP call
// fails, we log and fall back to a generic active reply so the SI
// lifecycle keeps advancing.
type OpenAI struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func NewOpenAI(endpoint, apiKey, model string) *OpenAI {
	endpoint = strings.TrimRight(endpoint, "/")
	if model == "" {
		// Sensible default for local Ollama installs. Override in config.
		model = "llama3.2"
	}
	return &OpenAI{
		endpoint: endpoint,
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (o *OpenAI) Reply(req ReplyRequest) ReplyResponse {
	// Handoff intent short-circuits the LLM call: deterministic message,
	// deterministic URL, no token cost, no latency. Mirrors Mock.
	if ok, handoff, msg := detectHandoff(req.UserText, req.BrandName, req.BrandDomain, req.OfferingID); ok {
		return ReplyResponse{
			Message:       msg,
			SessionStatus: "pending_handoff",
			HandoffURL:    handoff,
		}
	}

	messages := buildMessages(req)
	body, err := json.Marshal(chatRequest{Model: o.model, Messages: messages, Stream: false})
	if err != nil {
		return o.fallback("marshal request: " + err.Error())
	}

	httpReq, err := http.NewRequest(http.MethodPost, o.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return o.fallback("build request: " + err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return o.fallback("http: " + err.Error())
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return o.fallback("read: " + err.Error())
	}
	if resp.StatusCode/100 != 2 {
		return o.fallback(fmt.Sprintf("status=%d body=%s", resp.StatusCode, truncate(string(raw), 200)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return o.fallback("decode: " + err.Error())
	}
	if parsed.Error != nil {
		return o.fallback("upstream: " + parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return o.fallback("empty choice")
	}

	return ReplyResponse{
		Message:       strings.TrimSpace(parsed.Choices[0].Message.Content),
		SessionStatus: "active",
	}
}

// buildMessages assembles a chat-completions message slice: one system
// turn with brand framing + a compact catalog summary, then the full
// turn log from the session store mapped to OpenAI roles.
func buildMessages(req ReplyRequest) []chatMessage {
	msgs := make([]chatMessage, 0, len(req.History)+1)
	msgs = append(msgs, chatMessage{Role: "system", Content: systemPrompt(req)})
	for _, t := range req.History {
		role := "user"
		if t.Role == "brand" {
			role = "assistant"
		}
		msgs = append(msgs, chatMessage{Role: role, Content: t.Content})
	}
	return msgs
}

func systemPrompt(req ReplyRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the brand agent for %s (%s). Answer questions about the brand's catalog, recommend products, and stay concise (1-3 short sentences). ", req.BrandName, req.BrandDomain)
	b.WriteString("Only recommend products listed below. If the user signals buy intent, acknowledge it briefly — the host will detect intent and route to checkout separately.\n\n")
	b.WriteString("Catalog:\n")
	limit := len(req.Catalog)
	if limit > 20 {
		limit = 20
	}
	for i := 0; i < limit; i++ {
		p := req.Catalog[i]
		fmt.Fprintf(&b, "- %s (%s): %s", p.Name, p.ID, p.Description)
		if p.Price > 0 {
			fmt.Fprintf(&b, " [%s]", formatPrice(p.Price, p.Currency))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (o *OpenAI) fallback(reason string) ReplyResponse {
	log.Printf("[llm:openai] fallback reason=%q endpoint=%s model=%s", reason, o.endpoint, o.model)
	return ReplyResponse{
		Message:       "I'm having trouble reaching my catalog right now — could you try that again in a moment?",
		SessionStatus: "active",
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Compile-time interface check.
var _ Provider = (*OpenAI)(nil)
var _ Provider = (*Mock)(nil)
