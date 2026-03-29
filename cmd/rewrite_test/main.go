package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/rewrite"
	"github.com/LingByte/Ling/pkg/utils"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("MODE")))
	if mode == "" {
		mode = rewrite.ModeRule
	}
	q := strings.TrimSpace(utils.GetEnv("QUERY"))
	if q == "" {
		q = "如何提高Python代码效率"
	}
	maxChars := 200

	var opts *rewrite.FactoryOptions
	req := rewrite.RewriteRequest{Query: q, Mode: mode, MaxChars: maxChars}
	if mode == rewrite.ModeLLM {
		opts = &rewrite.FactoryOptions{LLM: &fakeLLM{resp: `{"rewritten":"Python 代码优化 循环效率 内存管理 常用库 性能对比","keywords":["Python","代码优化","性能"]}`}, LLMModel: "fake"}
		req.LLMModel = "fake"
	}

	rw, err := rewrite.New(mode, opts)
	if err != nil {
		fmt.Printf("new rewriter failed: %v\n", err)
		return
	}

	resp, err := rw.Rewrite(ctx, req)
	if err != nil {
		fmt.Printf("rewrite failed: %v\n", err)
		return
	}

	fmt.Printf("mode=%s\n", mode)
	fmt.Printf("original=%q\n", resp.Original)
	fmt.Printf("rewritten=%q\n", resp.Rewritten)
	fmt.Printf("keywords=%v\n", resp.Keywords)
	fmt.Printf("latency=%s\n", resp.Latency)
	if len(resp.Debug) > 0 {
		fmt.Printf("debug=%v\n", resp.Debug)
	}
}
