// Package linter implements a linter that validates Go API type definitions
// against Kubernetes API conventions as described in:
// https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md
//
// It is designed for APIs implemented with the orlop framework.
package linter

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity classifies how serious a violation is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Violation is a single finding produced by a Rule.
type Violation struct {
	// Rule is the identifier of the rule that produced this violation.
	Rule string
	// Severity indicates whether this is an error or a warning.
	Severity Severity
	// File is the source file where the violation was found.
	File string
	// Line is the 1-indexed line number within File.
	Line int
	// Message describes the problem.
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s: %s", v.File, v.Line, v.Severity, v.Rule, v.Message)
}

// Rule is implemented by each individual linting check.
type Rule interface {
	// ID returns a short, stable identifier for the rule (e.g. "no-bool-fields").
	ID() string
	// Check is called once per Go source file and should append any violations
	// it finds to the provided slice.
	Check(fset *token.FileSet, file *ast.File, path string) []Violation
}

// Linter runs a set of Rules over one or more directories.
type Linter struct {
	rules []Rule
}

// New creates a Linter configured with all built-in rules.
func New() *Linter {
	return &Linter{
		rules: defaultRules(),
	}
}

// NewWithRules creates a Linter with a custom set of rules.
func NewWithRules(rules []Rule) *Linter {
	return &Linter{rules: rules}
}

// FilteredRules returns a subset of the linter's rules whose IDs are in the
// allowed map.
func (l *Linter) FilteredRules(allowed map[string]bool) []Rule {
	var out []Rule
	for _, r := range l.rules {
		if allowed[r.ID()] {
			out = append(out, r)
		}
	}
	return out
}

// Run analyses all Go files found under every path in dirs (recursively)
// and returns the collected violations sorted by file and line.
func (l *Linter) Run(dirs []string) ([]Violation, error) {
	var violations []Violation

	for _, dir := range dirs {
		v, err := l.runDir(dir)
		if err != nil {
			return violations, err
		}
		violations = append(violations, v...)
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})

	return violations, nil
}

func (l *Linter) runDir(dir string) ([]Violation, error) {
	var violations []Violation

	fset := token.NewFileSet()

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip generated files.
		if strings.HasPrefix(filepath.Base(path), "zz_generated.") {
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}

		for _, rule := range l.rules {
			violations = append(violations, rule.Check(fset, f, path)...)
		}
		return nil
	})

	return violations, err
}

// defaultRules returns the full set of built-in rules.
func defaultRules() []Rule {
	return []Rule{
		&RuleOptionalFieldsNeedOmitempty{},
		&RuleRequiredFieldsNoOmitempty{},
		&RuleOptionalFieldsNeedPointerOrNilable{},
		&RuleNoBoolFields{},
		&RuleNoIntFields{},
		&RuleNoFloatInSpec{},
		&RuleNoUnsignedInts{},
		&RuleJSONTagCamelCase{},
		&RuleGoFieldPascalCase{},
		&RuleNoIsFooable{},
		&RuleTimestampSuffix{},
		&RuleDurationSeconds{},
		&RuleNoEnumNumeric{},
		&RuleEnumValuesCamelCase{},
		&RuleSpecStatusTopLevel{},
		&RuleStatusConditionsListType{},
		&RuleConditionsMetav1Type{},
		&RuleNoPhaseField{},
		&RuleListKindSuffix{},
		&RuleObjectMetaEmbedded{},
		&RuleTypemetaEmbedded{},
		&RuleSubresourceStatusMarker{},
		&RuleOptionalRequired{},
		&RuleStringFieldsMaxLength{},
		&RuleListFieldsListType{},
		&RuleNoAbbreviations{},
	}
}
