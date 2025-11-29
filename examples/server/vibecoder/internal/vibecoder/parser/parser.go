package parser

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type Language string

const (
	LangTypeScript Language = "typescript"
	LangJava       Language = "java"
	LangUnknown    Language = "unknown"
)

type StepDefFound struct {
	Pattern      string
	FunctionName string
	Line         int
}

func DetectLanguage(filename string) Language {
	if strings.HasSuffix(filename, ".ts") || strings.HasSuffix(filename, ".tsx") {
		return LangTypeScript
	}
	if strings.HasSuffix(filename, ".java") {
		return LangJava
	}
	return LangUnknown
}

func ParseImports(content []byte, lang Language) ([]string, error) {
	var sl *sitter.Language
	switch lang {
	case LangTypeScript:
		sl = typescript.GetLanguage()
	case LangJava:
		sl = java.GetLanguage()
	default:
		return nil, nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(sl)

	tree, _ := parser.ParseCtx(context.Background(), nil, content)
	root := tree.RootNode()

	var queryStr string
	if lang == LangTypeScript {
		queryStr = `
		(import_statement source: (string (string_fragment) @path))
		(export_statement source: (string (string_fragment) @path))
		`
	} else if lang == LangJava {
		// Java imports are usually package names, not file paths.
		// But for analysis we collect them.
		queryStr = `(import_declaration (scoped_identifier) @path)`
	}

	q, err := sitter.NewQuery([]byte(queryStr), sl)
	if err != nil {
		return nil, err
	}
	qc := sitter.NewQueryCursor()
	qc.Exec(q, root)

	var imports []string
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		for _, c := range m.Captures {
			if c.Node != nil {
				text := string(content[c.Node.StartByte():c.Node.EndByte()])
				imports = append(imports, text)
			}
		}
	}

	return imports, nil
}

func ParseStepDefinitions(content []byte, lang Language) ([]StepDefFound, error) {
	var sl *sitter.Language
	switch lang {
	case LangTypeScript:
		sl = typescript.GetLanguage()
	case LangJava:
		sl = java.GetLanguage()
	default:
		return nil, nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(sl)
	tree, _ := parser.ParseCtx(context.Background(), nil, content)
	root := tree.RootNode()

	// Heuristic queries for Cucumber steps
	// Note: these are simplified and might need tuning for specific frameworks
	var queryStr string
	if lang == LangTypeScript {
		// Matches: Given("pattern", function() {})
		queryStr = `
		(call_expression
			function: (identifier) @keyword
			arguments: (arguments
				(string (string_fragment) @pattern)
			)
		)
		`
	} else if lang == LangJava {
		// Matches: @Given("pattern") public void method()
		queryStr = `
		(method_declaration
			(modifiers
				(marker_annotation
					name: (identifier) @keyword
					arguments: (argument_list (string (string_fragment) @pattern))
				)
			)
			name: (identifier) @method
		)
		`
	}

	q, err := sitter.NewQuery([]byte(queryStr), sl)
	if err != nil {
		return nil, err
	}
	qc := sitter.NewQueryCursor()
	qc.Exec(q, root)

	var results []StepDefFound

	// We need to group captures by match to keep keyword, pattern, and method together
	// iterate matches
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		var pattern, method string
		var line int

		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			if name == "pattern" {
				pattern = string(content[c.Node.StartByte():c.Node.EndByte()])
				line = int(c.Node.StartPoint().Row) + 1
			} else if name == "method" {
				method = string(content[c.Node.StartByte():c.Node.EndByte()])
			}
		}

		// Filter for Gherkin keywords if needed?
		// The query assumes specific structure.
		if pattern != "" {
			results = append(results, StepDefFound{
				Pattern:      pattern,
				FunctionName: method,
				Line:         line,
			})
		}
	}

	return results, nil
}
