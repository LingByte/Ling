package censor

import "regexp"

type Rule struct {
	Name     string
	Category Category
	Severity Severity
	Pattern  *regexp.Regexp
	Action   Action
}

func DefaultRules() []Rule {
	// Keep defaults minimal and safe; callers can extend.
	return []Rule{
		{
			Name:     "email",
			Category: CategoryPII,
			Severity: SeverityMedium,
			Pattern:  regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
			Action:   ActionRedact,
		},
		{
			Name:     "cn_mobile",
			Category: CategoryPII,
			Severity: SeverityMedium,
			Pattern:  regexp.MustCompile(`\b1[3-9]\d{9}\b`),
			Action:   ActionRedact,
		},
		{
			Name:     "cn_id",
			Category: CategoryPII,
			Severity: SeverityHigh,
			Pattern:  regexp.MustCompile(`\b\d{17}[0-9Xx]\b`),
			Action:   ActionBlock,
		},
		{
			Name:     "bank_card_like",
			Category: CategoryPII,
			Severity: SeverityHigh,
			Pattern:  regexp.MustCompile(`\b\d{13,19}\b`),
			Action:   ActionBlock,
		},
		{
			Name:     "weapons",
			Category: CategoryViolence,
			Severity: SeverityHigh,
			Pattern:  regexp.MustCompile(`(?i)\b(炸弹|爆炸物|自制炸弹|枪支|手枪|步枪|子弹|手榴弹)\b`),
			Action:   ActionBlock,
		},
		{
			Name:     "suicide",
			Category: CategoryViolence,
			Severity: SeverityHigh,
			Pattern:  regexp.MustCompile(`(?i)\b(自杀|轻生|结束生命)\b`),
			Action:   ActionBlock,
		},
		{
			Name:     "fraud_keywords",
			Category: CategoryFraud,
			Severity: SeverityHigh,
			Pattern:  regexp.MustCompile(`(?i)\b(刷流水|套现|网赌|博彩|诈骗|洗钱)\b`),
			Action:   ActionBlock,
		},
	}
}
