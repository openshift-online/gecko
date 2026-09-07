// orlop-lint validates Go API type definitions used with the orlop framework
// against Kubernetes API conventions.
//
// Usage:
//
//	orlop-lint [flags] [dir...]
//
// If no directories are provided the current directory is linted.
//
// Flags:
//
//	-errors-only   only print violations with severity "error"
//	-json          output violations as JSON (one object per line)
//	-rules         comma-separated list of rule IDs to run (default: all)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openshift-online/gecko/orlop/pkg/linter"
)

func main() {
	var (
		errorsOnly  bool
		jsonOutput  bool
		ruleFilter  string
	)

	flag.BoolVar(&errorsOnly, "errors-only", false, "only print violations with severity 'error'")
	flag.BoolVar(&jsonOutput, "json", false, "output violations as JSON (one object per line)")
	flag.StringVar(&ruleFilter, "rules", "", "comma-separated list of rule IDs to run (empty = all)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: orlop-lint [flags] [dir...]\n\n")
		fmt.Fprintf(os.Stderr, "Validates API type definitions against Kubernetes API conventions.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	l := linter.New()

	if ruleFilter != "" {
		allowed := make(map[string]bool)
		for _, id := range strings.Split(ruleFilter, ",") {
			allowed[strings.TrimSpace(id)] = true
		}
		l = linter.NewWithRules(filterRules(linter.New(), allowed))
	}

	violations, err := l.Run(dirs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	hasError := false
	for _, v := range violations {
		if errorsOnly && v.Severity != linter.SeverityError {
			continue
		}

		if jsonOutput {
			b, _ := json.Marshal(map[string]any{
				"rule":     v.Rule,
				"severity": string(v.Severity),
				"file":     v.File,
				"line":     v.Line,
				"message":  v.Message,
			})
			fmt.Println(string(b))
		} else {
			fmt.Println(v)
		}

		if v.Severity == linter.SeverityError {
			hasError = true
		}
	}

	if hasError {
		os.Exit(1)
	}
}

// filterRules returns only the rules whose IDs are in allowed.
// It pulls the full rule list from a fresh Linter via reflection-free introspection.
func filterRules(l *linter.Linter, allowed map[string]bool) []linter.Rule {
	// We need access to the rules slice; expose it via a helper on Linter.
	return l.FilteredRules(allowed)
}
