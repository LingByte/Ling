package llm

func copyOptions(src *LLMOptions) LLMOptions {
	if src == nil {
		return LLMOptions{}
	}
	dst := *src
	return dst
}

type llmMemoryMessage struct {
	Role    string
	Content string
}

