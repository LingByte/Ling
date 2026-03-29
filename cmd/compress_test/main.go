package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/utils"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("MODE")))
	if mode == "" {
		mode = compress.ModeRule
	}
	q := strings.TrimSpace(utils.GetEnv("QUERY"))
	if q == "" {
		q = "如何提高Python代码效率"
	}
	maxChars := getEnvInt("MAX_CHARS", 400)

	items := []compress.Item{
		{ID: "doc#1", Title: "doc1", Content: "Python 循环效率可以通过列表推导式、内置函数（map/sum）提升。内存管理可以用生成器减少峰值。", Tags: []string{"python", "性能优化"}},
		{ID: "doc#2", Title: "doc2", Content: "如果要做性能对比，可以使用 timeit 或 cProfile 进行基准测试。", Tags: []string{"benchmark"}},
	}

	var opts *compress.FactoryOptions
	req := compress.CompressRequest{Query: q, Items: items, Mode: mode, MaxChars: maxChars}
	if mode == compress.ModeLLM {
		opts = &compress.FactoryOptions{LLM: &fakeLLM{resp: "- 循环: 优先列表推导式/内置函数\n- 内存: 生成器降低峰值\n- 评测: timeit/cProfile"}}
		req.LLMModel = strings.TrimSpace(utils.GetEnv("LLM_MODEL"))
		if req.LLMModel == "" {
			req.LLMModel = "fake"
		}
	}

	c, err := compress.New(mode, opts)
	if err != nil {
		fmt.Printf("new compressor failed: %v\n", err)
		return
	}

	resp, err := c.Compress(ctx, req)
	if err != nil {
		fmt.Printf("compress failed: %v\n", err)
		return
	}

	fmt.Printf("mode=%s maxChars=%d\n", mode, maxChars)
	fmt.Printf("originalChars=%d compressedChars=%d\n", resp.OriginalChars, resp.CompressedChars)
	fmt.Printf("compressed:\n%s\n", resp.Compressed)
	if len(resp.Debug) > 0 {
		fmt.Printf("debug=%v\n", resp.Debug)
	}
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(utils.GetEnv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
