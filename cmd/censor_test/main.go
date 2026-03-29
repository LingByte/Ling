package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/censor"
	"github.com/LingByte/Ling/pkg/utils"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("MODE")))
	if mode == "" {
		mode = censor.ModeRule
	}
	text := strings.TrimSpace(utils.GetEnv("TEXT"))
	if text == "" {
		text = "联系我 a@b.com 或 13800138000"
	}
	dictPath := strings.TrimSpace(utils.GetEnv("KEYWORD_DICT"))

	var opts *censor.FactoryOptions
	if mode == censor.ModeLLM {
		opts = &censor.FactoryOptions{LLM: &fakeLLM{resp: `{"action":"allow","category":"other","severity":"low","reason":"ok"}`}, LLMModel: "fake"}
	} else if dictPath != "" {
		opts = &censor.FactoryOptions{KeywordDictPath: dictPath}
	}

	c, err := censor.New(mode, opts)
	if err != nil {
		fmt.Printf("new censor failed: %v\n", err)
		return
	}

	resp, err := c.Assess(ctx, censor.AssessRequest{Text: text, Mode: mode})
	if err != nil {
		fmt.Printf("assess error: %v\n", err)
	}
	fmt.Printf("mode=%s\n", mode)
	fmt.Printf("action=%s allowed=%v redacted=%v blocked=%v\n", resp.Action, resp.Allowed, resp.Redacted, resp.Blocked)
	fmt.Printf("processed=%q\n", resp.Processed)
	if len(resp.Matches) > 0 {
		fmt.Printf("matches=%v\n", resp.Matches)
	}
	if len(resp.Debug) > 0 {
		fmt.Printf("debug=%v\n", resp.Debug)
	}
}
