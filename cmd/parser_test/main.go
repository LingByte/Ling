package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LingByte/Ling/pkg/parser"
)

func main() {
	ctx := context.Background()
	root := filepath.Join("cmd", "parser_test")
	fixtures := filepath.Join(root, "fixtures")

	entries, err := os.ReadDir(fixtures)
	if err != nil {
		fmt.Printf("failed to read fixtures dir %s: %v\n", fixtures, err)
		os.Exit(1)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	fmt.Printf("Parser unified entrypoint test runner\n")
	fmt.Printf("Fixtures: %s\n\n", fixtures)

	opts := &parser.ParseOptions{MaxTextLength: 800, PreserveLineBreaks: true}
	for _, name := range names {
		path := filepath.Join(fixtures, name)
		req := &parser.ParseRequest{Path: path, FileName: name}
		ft := parser.DetectFileType(req)
		fmt.Printf("==> %s (detected=%s)\n", name, ft)

		var res *parser.ParseResult
		var perr error
		// Demonstrate both ParsePath and ParseBytes usage.
		if strings.HasSuffix(strings.ToLower(name), ".json") {
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				fmt.Printf("read failed: %v\n\n", rerr)
				continue
			}
			res, perr = parser.ParseBytes(ctx, name, b, opts)
		} else {
			res, perr = parser.ParsePath(ctx, path, opts)
		}

		if perr != nil {
			fmt.Printf("parse failed: %v\n\n", perr)
			continue
		}

		preview := res.Text
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		fmt.Printf("parsed: fileType=%s sections=%d chars=%d\n", res.FileType, len(res.Sections), len(res.Text))
		fmt.Printf("preview:\n%s\n\n", preview)
	}
}
