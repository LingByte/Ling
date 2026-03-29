package censor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type LLMCensor struct {
	LLM    LLM
	Model  string
	Policy *Policy
}

type llmAssessment struct {
	Action   string     `json:"action"`
	Category Category   `json:"category"`
	Severity Severity   `json:"severity"`
	Reason   string     `json:"reason"`
}

func (c *LLMCensor) Assess(ctx context.Context, req AssessRequest) (*AssessResponse, error) {
	_ = ctx
	start := time.Now()
	text := normalizeText(req.Text)
	if text == "" {
		return nil, ErrEmptyText
	}
	if c == nil || c.LLM == nil {
		return nil, ErrMissingLLM
	}

	pol := req.Policy
	if pol == nil {
		pol = c.Policy
	}
	if pol == nil {
		pol = DefaultPolicy()
	}

	model := strings.TrimSpace(c.Model)
	if req.Options != nil {
		if m, ok := req.Options["model"].(string); ok && strings.TrimSpace(m) != "" {
			model = strings.TrimSpace(m)
		}
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	prompt := buildModerationPrompt(text)
	out, err := c.LLM.Query(prompt, model)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, errors.New("llm returned empty moderation")
	}

	var a llmAssessment
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		// If output isn't JSON, treat as allow to avoid hard blocks caused by parsing.
		return &AssessResponse{
			Action:    ActionAllow,
			Allowed:   true,
			Original:  text,
			Processed: text,
			Strategy:  ModeLLM,
			Latency:   time.Since(start),
			Debug: map[string]any{
				"raw": out,
				"parse_error": err.Error(),
			},
		}, nil
	}

	action := Action(strings.ToLower(strings.TrimSpace(a.Action)))
	if action == "" {
		action = pol.DefaultAction
	}

	processed := text
	if action == ActionRedact {
		mask := pol.RedactionMask
		if strings.TrimSpace(mask) == "" {
			mask = "[REDACTED]"
		}
		processed = mask
	}

	matches := []Match{}
	if a.Category != "" || a.Severity != "" {
		matches = append(matches, Match{Category: a.Category, Severity: a.Severity, Rule: "llm", Span: ""})
	}

	resp := &AssessResponse{
		Action:    action,
		Allowed:   action != ActionBlock,
		Redacted:  action == ActionRedact,
		Blocked:   action == ActionBlock,
		Original:  text,
		Processed: processed,
		Matches:   matches,
		Strategy:  ModeLLM,
		Latency:   time.Since(start),
		Debug: map[string]any{
			"raw": out,
			"reason": strings.TrimSpace(a.Reason),
		},
	}
	if resp.Blocked {
		return resp, ErrBlocked
	}
	return resp, nil
}

func buildModerationPrompt(text string) string {
	// Expect strict JSON output.
	return "你是内容安全审核器。请对用户文本进行安全审核，只输出JSON，不要解释。\n" +
		"JSON schema: {\"action\":\"allow|redact|block\",\"category\":\"pii|violence|sexual|hate|fraud|other\",\"severity\":\"low|medium|high\",\"reason\":\"...\"}\n" +
		"文本:\n" + text + "\n"
}
