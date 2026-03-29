package censor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func TestRuleCensor_RedactEmail(t *testing.T) {
	c := &RuleCensor{Policy: DefaultPolicy(), Rules: DefaultRules()}
	resp, err := c.Assess(context.Background(), AssessRequest{Text: "contact me at a@b.com", Mode: ModeRule})
	if err != nil {
		// redaction should not error
		t.Fatalf("Assess error: %v", err)
	}
	if !resp.Redacted {
		t.Fatalf("expected redacted")
	}
	if resp.Processed == resp.Original {
		t.Fatalf("expected processed to differ")
	}
}

func TestRuleCensor_BlockCNID(t *testing.T) {
	c := &RuleCensor{Policy: DefaultPolicy(), Rules: DefaultRules()}
	resp, err := c.Assess(context.Background(), AssessRequest{Text: "身份证 11010519491231002X", Mode: ModeRule})
	if err == nil {
		t.Fatalf("expected error")
	}
	if resp == nil || !resp.Blocked {
		t.Fatalf("expected blocked")
	}
}

func TestLLMCensor_AllowJSON(t *testing.T) {
	c := &LLMCensor{LLM: &fakeLLM{resp: `{"action":"allow","category":"other","severity":"low","reason":"ok"}`}, Model: "fake", Policy: DefaultPolicy()}
	resp, err := c.Assess(context.Background(), AssessRequest{Text: "hello", Mode: ModeLLM})
	if err != nil {
		t.Fatalf("Assess error: %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("expected allowed")
	}
}

func TestKeywordDict_Block(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "dict.txt")
	// keyword only -> defaults to block/high/other
	if err := os.WriteFile(path, []byte("赌博\n"), 0o600); err != nil {
		t.Fatalf("write dict: %v", err)
	}
	kr, err := LoadKeywordDict(path, false)
	if err != nil {
		t.Fatalf("LoadKeywordDict: %v", err)
	}
	c := &RuleCensor{Policy: DefaultPolicy(), Rules: nil, KeywordRules: kr}
	resp, err := c.Assess(context.Background(), AssessRequest{Text: "这里有赌博内容", Mode: ModeRule})
	if err == nil {
		t.Fatalf("expected blocked error")
	}
	if resp == nil || !resp.Blocked {
		t.Fatalf("expected blocked")
	}
}
