package censor

import (
	"context"
	"strings"
	"time"
)

type RuleCensor struct {
	Policy *Policy
	Rules  []Rule
	KeywordRules []KeywordRule
}

func (c *RuleCensor) Assess(ctx context.Context, req AssessRequest) (*AssessResponse, error) {
	_ = ctx
	start := time.Now()
	text := normalizeText(req.Text)
	if text == "" {
		return nil, ErrEmptyText
	}
	pol := req.Policy
	if pol == nil {
		pol = c.Policy
	}
	if pol == nil {
		pol = DefaultPolicy()
	}
	mask := pol.RedactionMask
	if strings.TrimSpace(mask) == "" {
		mask = "[REDACTED]"
	}

	rules := c.Rules
	if len(rules) == 0 {
		rules = DefaultRules()
	}
	keywordRules := c.KeywordRules

	matches := make([]Match, 0)
	action := pol.DefaultAction
	maxSeverity := Severity("")

	processed := text

	for _, r := range rules {
		if r.Pattern == nil {
			continue
		}
		locs := r.Pattern.FindAllStringIndex(processed, -1)
		if len(locs) == 0 {
			continue
		}
		for _, loc := range locs {
			span := ""
			if loc[0] >= 0 && loc[1] <= len(processed) && loc[0] < loc[1] {
				span = processed[loc[0]:loc[1]]
			}
			matches = append(matches, Match{Category: r.Category, Severity: r.Severity, Rule: r.Name, Span: span})
			if severityRank(r.Severity) > severityRank(maxSeverity) {
				maxSeverity = r.Severity
			}
			// Determine action based on rule/action and policy thresholds.
			if r.Action == ActionBlock {
				action = ActionBlock
			} else if r.Action == ActionRedact && action != ActionBlock {
				action = ActionRedact
			}
		}

		// Apply redaction per rule if required.
		if r.Action == ActionRedact {
			processed = r.Pattern.ReplaceAllString(processed, mask)
		}
	}

	for _, r := range keywordRules {
		if r.Pattern == nil {
			continue
		}
		locs := r.Pattern.FindAllStringIndex(processed, -1)
		if len(locs) == 0 {
			continue
		}
		for _, loc := range locs {
			span := ""
			if loc[0] >= 0 && loc[1] <= len(processed) && loc[0] < loc[1] {
				span = processed[loc[0]:loc[1]]
			}
			matches = append(matches, Match{Category: r.Category, Severity: r.Severity, Rule: r.Name, Span: span})
			if severityRank(r.Severity) > severityRank(maxSeverity) {
				maxSeverity = r.Severity
			}
			if r.Action == ActionBlock {
				action = ActionBlock
			} else if r.Action == ActionRedact && action != ActionBlock {
				action = ActionRedact
			}
		}
		if r.Action == ActionRedact {
			processed = r.Pattern.ReplaceAllString(processed, mask)
		}
	}

	// Enforce policy thresholds.
	if maxSeverity != "" {
		if severityGE(maxSeverity, pol.BlockAtOrAbove) {
			action = ActionBlock
		} else if severityGE(maxSeverity, pol.RedactAtOrAbove) && action != ActionBlock {
			action = ActionRedact
		}
	}

	resp := &AssessResponse{
		Action:    action,
		Allowed:   action != ActionBlock,
		Redacted:  action == ActionRedact,
		Blocked:   action == ActionBlock,
		Original:  text,
		Processed: processed,
		Matches:   matches,
		Strategy:  ModeRule,
		Latency:   time.Since(start),
		Debug: map[string]any{
			"match_count": len(matches),
			"keyword_rules": len(keywordRules),
		},
	}
	if resp.Blocked {
		return resp, ErrBlocked
	}
	return resp, nil
}
