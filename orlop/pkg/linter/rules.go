package linter

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"unicode"
)

// ─────────────────────────────────────────────────────────────────────────────
// Rule: optional-omitempty
//
// K8s API conventions: optional fields MUST have the `omitempty` json tag.
// ─────────────────────────────────────────────────────────────────────────────

// RuleOptionalFieldsNeedOmitempty flags optional fields that are missing omitempty.
type RuleOptionalFieldsNeedOmitempty struct{}

func (r *RuleOptionalFieldsNeedOmitempty) ID() string { return "optional-omitempty" }

func (r *RuleOptionalFieldsNeedOmitempty) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			if !isOptional(f) {
				return
			}
			if hasOmitempty(f) {
				return
			}
			// If it is explicitly marked optional but lacks omitempty on json tag, flag it.
			if hasMarkerInGroup(f, "+optional") {
				file2, line := positionString(fset, f.Pos())
				_ = file2
				for _, name := range fieldNames(f) {
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s is marked +optional but json tag is missing `omitempty`", ts.Name.Name, name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: required-no-omitempty
//
// K8s API conventions: required fields must NOT have omitempty.
// ─────────────────────────────────────────────────────────────────────────────

type RuleRequiredFieldsNoOmitempty struct{}

func (r *RuleRequiredFieldsNoOmitempty) ID() string { return "required-no-omitempty" }

func (r *RuleRequiredFieldsNoOmitempty) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) || !isRequired(f) {
				return
			}
			if hasOmitempty(f) {
				_, line := positionString(fset, f.Pos())
				for _, name := range fieldNames(f) {
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s is marked +required but json tag has `omitempty`; required fields should not be omitted", ts.Name.Name, name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: optional-pointer
//
// K8s API conventions: optional fields that are not maps/slices should be
// pointer types so that unset can be distinguished from the zero value.
// ─────────────────────────────────────────────────────────────────────────────

type RuleOptionalFieldsNeedPointerOrNilable struct{}

func (r *RuleOptionalFieldsNeedPointerOrNilable) ID() string { return "optional-pointer" }

func (r *RuleOptionalFieldsNeedPointerOrNilable) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			if !isOptional(f) && !hasOmitempty(f) {
				return
			}
			// Already nilable (pointer, slice, map) → OK.
			if isNilable(f.Type) {
				return
			}
			// Primitive scalar types: bool, int32, int64, string, float64 that are
			// optional should be pointers unless the zero value is meaningless and
			// a kubebuilder validation guards it.
			tn := baseTypeName(f.Type)
			primitives := map[string]bool{
				"bool": true, "int32": true, "int64": true,
				"string": true, "float64": true, "float32": true,
			}
			if !primitives[tn] {
				// Struct type: optional structs that have no required sub-fields may
				// be pointers; warn only if they're not a pointer.
				if !isPointerType(f.Type) {
					_, line := positionString(fset, f.Pos())
					for _, name := range fieldNames(f) {
						vs = append(vs, Violation{
							Rule:     r.ID(),
							Severity: SeverityWarning,
							File:     path,
							Line:     line,
							Message:  fmt.Sprintf("field %s.%s is optional but its type %s is not a pointer or nilable type; consider using a pointer to distinguish 'unset' from 'zero value'", ts.Name.Name, name, typeName(f.Type)),
						})
					}
				}
				return
			}
			// bool is always ambiguous as a zero value.
			if tn == "bool" {
				_, line := positionString(fset, f.Pos())
				for _, name := range fieldNames(f) {
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s is a bool; consider using *bool with omitempty since false is a meaningful value", ts.Name.Name, name),
					})
				}
				return
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-bool-fields
//
// K8s API conventions: bool fields tend to become multi-value enums. Prefer
// a string type alias to allow future extensibility.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoBoolFields struct{}

func (r *RuleNoBoolFields) ID() string { return "no-bool-fields" }

func (r *RuleNoBoolFields) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			tn := baseTypeName(f.Type)
			// Allow *bool (pointer) — it's less bad — but still warn for plain bool.
			raw := typeName(f.Type)
			if raw == "bool" {
				_, line := positionString(fset, f.Pos())
				for _, name := range fieldNames(f) {
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s uses type bool; consider a string type alias (e.g. EnabledState) to allow future extensibility per K8s API conventions", ts.Name.Name, name),
					})
				}
			}
			_ = tn
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-int-type
//
// K8s API conventions: all public integer fields MUST use int32 or int64,
// not the platform-ambiguous int type.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoIntFields struct{}

func (r *RuleNoIntFields) ID() string { return "no-int-type" }

func (r *RuleNoIntFields) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			raw := typeName(f.Type)
			base := baseTypeName(f.Type)
			if base == "int" || raw == "int" || raw == "*int" || raw == "[]int" {
				_, line := positionString(fset, f.Pos())
				for _, name := range fieldNames(f) {
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityError,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s uses type `int` which is ambiguously-sized; use int32 or int64 per K8s API conventions", ts.Name.Name, name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-float-in-spec
//
// K8s API conventions: avoid floating-point values; never use them in spec.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoFloatInSpec struct{}

func (r *RuleNoFloatInSpec) ID() string { return "no-float-in-spec" }

func (r *RuleNoFloatInSpec) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		// Heuristic: types whose name ends in "Spec" or whose parent is a Spec field.
		isSpecType := strings.HasSuffix(ts.Name.Name, "Spec")
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			base := baseTypeName(f.Type)
			isFloat := base == "float32" || base == "float64"
			if !isFloat {
				return
			}
			sev := SeverityWarning
			msg := fmt.Sprintf("field uses floating-point type %s; avoid floats in the API as they cannot be reliably round-tripped", typeName(f.Type))
			if isSpecType {
				sev = SeverityError
				msg = fmt.Sprintf("field uses floating-point type %s in a Spec type; K8s API conventions prohibit floats in spec", typeName(f.Type))
			}
			_, line := positionString(fset, f.Pos())
			for _, name := range fieldNames(f) {
				vs = append(vs, Violation{
					Rule:     r.ID(),
					Severity: sev,
					File:     path,
					Line:     line,
					Message:  fmt.Sprintf("%s.%s: %s", ts.Name.Name, name, msg),
				})
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-unsigned-ints
//
// K8s API conventions: do not use unsigned integers due to inconsistent
// support across languages and libraries.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoUnsignedInts struct{}

func (r *RuleNoUnsignedInts) ID() string { return "no-unsigned-ints" }

func (r *RuleNoUnsignedInts) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	unsigned := map[string]bool{
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	}
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			base := baseTypeName(f.Type)
			if !unsigned[base] {
				return
			}
			_, line := positionString(fset, f.Pos())
			for _, name := range fieldNames(f) {
				vs = append(vs, Violation{
					Rule:     r.ID(),
					Severity: SeverityError,
					File:     path,
					Line:     line,
					Message:  fmt.Sprintf("field %s.%s uses unsigned integer type %s; use signed integers and validate non-negative if needed per K8s API conventions", ts.Name.Name, name, typeName(f.Type)),
				})
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: json-camelcase
//
// K8s API conventions: JSON field names MUST be camelCase.
// ─────────────────────────────────────────────────────────────────────────────

type RuleJSONTagCamelCase struct{}

func (r *RuleJSONTagCamelCase) ID() string { return "json-camelcase" }

func (r *RuleJSONTagCamelCase) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			name := jsonName(f)
			if name == "" || name == "-" || name == "inline" {
				return
			}
			if !isCamelCase(name) {
				_, line := positionString(fset, f.Pos())
				for _, fn := range fieldNames(f) {
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityError,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s has json name %q which is not camelCase per K8s API conventions", ts.Name.Name, fn, name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: go-pascalcase
//
// K8s API conventions: Go field names MUST be PascalCase.
// ─────────────────────────────────────────────────────────────────────────────

type RuleGoFieldPascalCase struct{}

func (r *RuleGoFieldPascalCase) ID() string { return "go-pascalcase" }

func (r *RuleGoFieldPascalCase) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			for _, n := range f.Names {
				if !isPascalCase(n.Name) {
					_, line := positionString(fset, n.Pos())
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityError,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s is not PascalCase per K8s API conventions", ts.Name.Name, n.Name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-is-prefix
//
// K8s API conventions: a field expressing a boolean property 'fooable' should
// be called `Fooable`, not `IsFooable`.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoIsFooable struct{}

func (r *RuleNoIsFooable) ID() string { return "no-is-prefix" }

func (r *RuleNoIsFooable) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			for _, n := range f.Names {
				if strings.HasPrefix(n.Name, "Is") && len(n.Name) > 2 && unicode.IsUpper(rune(n.Name[2])) {
					_, line := positionString(fset, n.Pos())
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s starts with 'Is'; per K8s API conventions name it %s instead", ts.Name.Name, n.Name, n.Name[2:]),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: timestamp-suffix
//
// K8s API conventions: a field specifying the time at which something occurs
// should be named `somethingTime`, not `somethingTimestamp` or `somethingAt`.
// ─────────────────────────────────────────────────────────────────────────────

type RuleTimestampSuffix struct{}

func (r *RuleTimestampSuffix) ID() string { return "timestamp-suffix" }

func (r *RuleTimestampSuffix) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			for _, n := range f.Names {
				lower := strings.ToLower(n.Name)
				// Flag names like FooAt, FooStamp, FooTimestamp
				if strings.HasSuffix(lower, "at") ||
					strings.HasSuffix(lower, "stamp") ||
					strings.HasSuffix(lower, "timestamp") {

					base := baseTypeName(f.Type)
					// Only flag if it looks like a time field (Time type or has "time"/"timestamp"
					// in the name but wrong suffix).
					if base == "Time" || strings.Contains(lower, "time") || strings.Contains(lower, "timestamp") {
						_, line := positionString(fset, n.Pos())
						vs = append(vs, Violation{
							Rule:     r.ID(),
							Severity: SeverityWarning,
							File:     path,
							Line:     line,
							Message:  fmt.Sprintf("field %s.%s: per K8s API conventions, time fields should be named `somethingTime` (e.g. %s)", ts.Name.Name, n.Name, suggestTimeName(n.Name)),
						})
					}
				}
			}
		})
	})
	return vs
}

func suggestTimeName(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range []string{"timestamp", "stamp", "at"} {
		if strings.HasSuffix(lower, suffix) {
			base := name[:len(name)-len(suffix)]
			// Capitalise first letter after removing suffix
			if base == "" {
				return "Time"
			}
			// Trim any trailing capitalisation artefacts
			base = strings.TrimRight(base, "A") // e.g. "CreatedAt" → "Created"
			return base + "Time"
		}
	}
	return name + "Time"
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: duration-seconds
//
// K8s API conventions: durations should use the `fooSeconds` convention.
// ─────────────────────────────────────────────────────────────────────────────

type RuleDurationSeconds struct{}

func (r *RuleDurationSeconds) ID() string { return "duration-seconds" }

func (r *RuleDurationSeconds) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			base := baseTypeName(f.Type)
			// Flag fields whose type is time.Duration (often aliased), or whose
			// name contains "duration", "timeout", "period", "interval", "deadline"
			// but does NOT end in "Seconds".
			durationIndicators := []string{"duration", "timeout", "period", "interval", "deadline"}
			lower := strings.ToLower(strings.Join(fieldNames(f), ""))
			hasIndicator := false
			for _, ind := range durationIndicators {
				if strings.Contains(lower, ind) {
					hasIndicator = true
					break
				}
			}

			if base == "Duration" || hasIndicator {
				// Acceptable: ends in "Seconds"
				allGood := true
				for _, n := range fieldNames(f) {
					if !strings.HasSuffix(n, "Seconds") {
						allGood = false
						break
					}
				}
				if !allGood {
					_, line := positionString(fset, f.Pos())
					for _, n := range fieldNames(f) {
						vs = append(vs, Violation{
							Rule:     r.ID(),
							Severity: SeverityWarning,
							File:     path,
							Line:     line,
							Message:  fmt.Sprintf("field %s.%s appears to be a duration; per K8s API conventions use `fooSeconds` (int32/int64) instead of time.Duration", ts.Name.Name, n),
						})
					}
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-enum-numeric
//
// K8s API conventions: do not use numeric enumerations. Use string aliases.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoEnumNumeric struct{}

func (r *RuleNoEnumNumeric) ID() string { return "no-enum-numeric" }

func (r *RuleNoEnumNumeric) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	// Look for fields that have a +kubebuilder:validation:Enum marker where the
	// values are numeric.
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			val, ok := markerValue(f.Doc, "+kubebuilder:validation:Enum=")
			if !ok {
				return
			}
			// Check if any enum value is numeric.
			for _, v := range strings.Split(val, ";") {
				v = strings.TrimSpace(v)
				if len(v) == 0 {
					continue
				}
				if isNumericString(v) {
					_, line := positionString(fset, f.Pos())
					for _, n := range fieldNames(f) {
						vs = append(vs, Violation{
							Rule:     r.ID(),
							Severity: SeverityError,
							File:     path,
							Line:     line,
							Message:  fmt.Sprintf("field %s.%s has numeric enum value %q; per K8s API conventions use string aliases for constants", ts.Name.Name, n, v),
						})
					}
					break
				}
			}
		})
	})
	return vs
}

func isNumericString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: enum-camelcase
//
// K8s API conventions: enum constants should be CamelCase with an initial
// uppercase letter.
// ─────────────────────────────────────────────────────────────────────────────

type RuleEnumValuesCamelCase struct{}

func (r *RuleEnumValuesCamelCase) ID() string { return "enum-camelcase" }

func (r *RuleEnumValuesCamelCase) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			val, ok := markerValue(f.Doc, "+kubebuilder:validation:Enum=")
			if !ok {
				return
			}
			for _, v := range strings.Split(val, ";") {
				v = strings.TrimSpace(v)
				if v == "" || isNumericString(v) {
					continue
				}
				// Allow all-uppercase acronyms (TCP, UDP, GCP) and proper names
				// (docker, systemd). Flag values with underscores or lowercase initial.
				if strings.ContainsRune(v, '_') {
					_, line := positionString(fset, f.Pos())
					for _, n := range fieldNames(f) {
						vs = append(vs, Violation{
							Rule:     r.ID(),
							Severity: SeverityWarning,
							File:     path,
							Line:     line,
							Message:  fmt.Sprintf("field %s.%s has enum value %q containing underscores; per K8s API conventions constants should be CamelCase", ts.Name.Name, n, v),
						})
					}
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: spec-status-top-level
//
// K8s API conventions: objects with both spec and status should not have
// additional top-level fields beyond standard metadata fields.
// ─────────────────────────────────────────────────────────────────────────────

type RuleSpecStatusTopLevel struct{}

func (r *RuleSpecStatusTopLevel) ID() string { return "spec-status-top-level" }

func (r *RuleSpecStatusTopLevel) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	// Standard top-level fields allowed per K8s conventions.
	allowed := map[string]bool{
		"TypeMeta":   true,
		"ObjectMeta": true,
		"Spec":       true,
		"Status":     true,
	}

	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		if !isRootType(decl) || isListKind(ts.Name.Name) {
			return
		}

		hasSpec, hasStatus := false, false
		for _, f := range st.Fields.List {
			for _, n := range f.Names {
				if n.Name == "Spec" {
					hasSpec = true
				}
				if n.Name == "Status" {
					hasStatus = true
				}
			}
		}
		if !hasSpec || !hasStatus {
			return
		}

		for _, f := range st.Fields.List {
			if isEmbedded(f) {
				continue
			}
			for _, n := range f.Names {
				if !allowed[n.Name] {
					_, line := positionString(fset, n.Pos())
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("type %s has both spec and status but also has extra top-level field %s; per K8s API conventions objects with spec+status should not have additional top-level fields", ts.Name.Name, n.Name),
					})
				}
			}
		}
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: conditions-list-type
//
// K8s API conventions: Conditions slice should have list-type and merge-key
// markers (// +listType=map, // +listMapKey=type).
// ─────────────────────────────────────────────────────────────────────────────

type RuleStatusConditionsListType struct{}

func (r *RuleStatusConditionsListType) ID() string { return "conditions-list-type" }

func (r *RuleStatusConditionsListType) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			for _, n := range f.Names {
				if n.Name != "Conditions" {
					continue
				}
				if !isSliceType(f.Type) {
					continue
				}
				hasListType := hasMarkerInGroup(f, "+listType=map")
				hasListMapKey := hasMarkerInGroup(f, "+listMapKey=type")
				if !hasListType || !hasListMapKey {
					_, line := positionString(fset, f.Pos())
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.Conditions should have `// +listType=map` and `// +listMapKey=type` markers per K8s API conventions for server-side apply merge support", ts.Name.Name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: conditions-metav1
//
// K8s API conventions: Conditions should use []metav1.Condition, not []string
// or custom types.
// ─────────────────────────────────────────────────────────────────────────────

type RuleConditionsMetav1Type struct{}

func (r *RuleConditionsMetav1Type) ID() string { return "conditions-metav1" }

func (r *RuleConditionsMetav1Type) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			for _, n := range f.Names {
				if n.Name != "Conditions" {
					continue
				}
				if !isSliceType(f.Type) {
					continue
				}
				inner := baseTypeName(f.Type)
				if inner != "Condition" {
					_, line := positionString(fset, f.Pos())
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityError,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.Conditions should be []metav1.Condition (got element type %q); per K8s API conventions use the standard Condition schema", ts.Name.Name, inner),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-phase
//
// K8s API conventions: the `phase` pattern is deprecated. Use Conditions.
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoPhaseField struct{}

func (r *RuleNoPhaseField) ID() string { return "no-phase" }

func (r *RuleNoPhaseField) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			for _, n := range f.Names {
				if strings.EqualFold(n.Name, "phase") {
					_, line := positionString(fset, n.Pos())
					vs = append(vs, Violation{
						Rule:     r.ID(),
						Severity: SeverityWarning,
						File:     path,
						Line:     line,
						Message:  fmt.Sprintf("field %s.%s uses the deprecated phase pattern; per K8s API conventions, use Conditions instead of phase (see k8s.io/issues/7856)", ts.Name.Name, n.Name),
					})
				}
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: list-kind-suffix
//
// K8s API conventions: list kinds MUST have a name ending in "List".
// Also, any type with +kubebuilder:object:root=true and an Items field should
// end in "List".
// ─────────────────────────────────────────────────────────────────────────────

type RuleListKindSuffix struct{}

func (r *RuleListKindSuffix) ID() string { return "list-kind-suffix" }

func (r *RuleListKindSuffix) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		// A type with an "Items" field should be a list kind.
		hasItems := false
		for _, f := range st.Fields.List {
			for _, n := range f.Names {
				if n.Name == "Items" {
					hasItems = true
				}
			}
		}
		if hasItems && !strings.HasSuffix(ts.Name.Name, "List") {
			_, line := positionString(fset, ts.Pos())
			vs = append(vs, Violation{
				Rule:     r.ID(),
				Severity: SeverityError,
				File:     path,
				Line:     line,
				Message:  fmt.Sprintf("type %s has an Items field and looks like a list type but its name does not end with 'List'; per K8s API conventions list kinds must end with 'List'", ts.Name.Name),
			})
		}
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: objectmeta-embedded
//
// K8s API conventions: every API object must embed metav1.ObjectMeta.
// ─────────────────────────────────────────────────────────────────────────────

type RuleObjectMetaEmbedded struct{}

func (r *RuleObjectMetaEmbedded) ID() string { return "objectmeta-embedded" }

func (r *RuleObjectMetaEmbedded) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		if !isRootType(decl) || isListKind(ts.Name.Name) {
			return
		}

		hasObjectMeta := false
		for _, f := range st.Fields.List {
			if !isEmbedded(f) {
				continue
			}
			if baseTypeName(f.Type) == "ObjectMeta" {
				hasObjectMeta = true
				break
			}
		}
		if !hasObjectMeta {
			_, line := positionString(fset, ts.Pos())
			vs = append(vs, Violation{
				Rule:     r.ID(),
				Severity: SeverityError,
				File:     path,
				Line:     line,
				Message:  fmt.Sprintf("root type %s does not embed metav1.ObjectMeta; every K8s API object must have standard metadata", ts.Name.Name),
			})
		}
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: typemeta-embedded
//
// K8s API conventions: all API objects must embed metav1.TypeMeta.
// ─────────────────────────────────────────────────────────────────────────────

type RuleTypemetaEmbedded struct{}

func (r *RuleTypemetaEmbedded) ID() string { return "typemeta-embedded" }

func (r *RuleTypemetaEmbedded) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		if !isRootType(decl) {
			return
		}

		hasTypeMeta := false
		for _, f := range st.Fields.List {
			if !isEmbedded(f) {
				continue
			}
			if baseTypeName(f.Type) == "TypeMeta" {
				hasTypeMeta = true
				break
			}
		}
		if !hasTypeMeta {
			_, line := positionString(fset, ts.Pos())
			vs = append(vs, Violation{
				Rule:     r.ID(),
				Severity: SeverityError,
				File:     path,
				Line:     line,
				Message:  fmt.Sprintf("root type %s does not embed metav1.TypeMeta; all API objects must have kind and apiVersion per K8s API conventions", ts.Name.Name),
			})
		}
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: subresource-status
//
// Objects with a Status field should declare +kubebuilder:subresource:status
// so that the /status subresource endpoint is served and spec/status have
// separate authorization scopes.
// ─────────────────────────────────────────────────────────────────────────────

type RuleSubresourceStatusMarker struct{}

func (r *RuleSubresourceStatusMarker) ID() string { return "subresource-status" }

func (r *RuleSubresourceStatusMarker) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		if !isRootType(decl) || isListKind(ts.Name.Name) {
			return
		}

		hasStatus := false
		for _, f := range st.Fields.List {
			for _, n := range f.Names {
				if n.Name == "Status" {
					hasStatus = true
				}
			}
		}

		if hasStatus && !hasKubebuilderSubresourceStatus(decl) {
			_, line := positionString(fset, ts.Pos())
			vs = append(vs, Violation{
				Rule:     r.ID(),
				Severity: SeverityWarning,
				File:     path,
				Line:     line,
				Message:  fmt.Sprintf("type %s has a Status field but is missing `+kubebuilder:subresource:status`; add this marker so that spec and status have separate authorization scopes per K8s API conventions", ts.Name.Name),
			})
		}
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: optional-required-declared
//
// K8s API conventions: all fields must be explicitly marked either +optional
// or +required. A missing declaration makes the API ambiguous.
// ─────────────────────────────────────────────────────────────────────────────

type RuleOptionalRequired struct{}

func (r *RuleOptionalRequired) ID() string { return "optional-required-declared" }

func (r *RuleOptionalRequired) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			// If a field has omitempty it is implicitly optional — allow that for
			// compatibility (the spec says omitempty implies optional).
			if hasOmitempty(f) {
				return
			}
			if isOptional(f) || isRequired(f) {
				return
			}
			// The field declares neither; flag it.
			_, line := positionString(fset, f.Pos())
			for _, n := range fieldNames(f) {
				vs = append(vs, Violation{
					Rule:     r.ID(),
					Severity: SeverityWarning,
					File:     path,
					Line:     line,
					Message:  fmt.Sprintf("field %s.%s has no +optional or +required marker; per K8s API conventions all fields must declare their optionality", ts.Name.Name, n),
				})
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: string-max-length
//
// K8s API conventions: all string fields should be checked for maximum length.
// ─────────────────────────────────────────────────────────────────────────────

type RuleStringFieldsMaxLength struct{}

func (r *RuleStringFieldsMaxLength) ID() string { return "string-max-length" }

func (r *RuleStringFieldsMaxLength) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			base := baseTypeName(f.Type)
			if base != "string" {
				return
			}
			// Has enum → implicit maximum length exists.
			if hasMarkerInGroup(f, "+kubebuilder:validation:Enum=") {
				return
			}
			// Has MaxLength → OK.
			if hasMarkerInGroup(f, "+kubebuilder:validation:MaxLength=") {
				return
			}
			// Has Pattern with an anchored regex → also implies bounded.
			if hasMarkerInGroup(f, "+kubebuilder:validation:Pattern=") {
				return
			}
			_, line := positionString(fset, f.Pos())
			for _, n := range fieldNames(f) {
				vs = append(vs, Violation{
					Rule:     r.ID(),
					Severity: SeverityWarning,
					File:     path,
					Line:     line,
					Message:  fmt.Sprintf("string field %s.%s has no MaxLength constraint; per K8s API conventions all string fields should have a maximum length", ts.Name.Name, n),
				})
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: list-list-type
//
// K8s API conventions: all list fields should have their +listType tag set.
// ─────────────────────────────────────────────────────────────────────────────

type RuleListFieldsListType struct{}

func (r *RuleListFieldsListType) ID() string { return "list-list-type" }

func (r *RuleListFieldsListType) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			if !isSliceType(f.Type) {
				return
			}
			// Conditions is handled by a dedicated rule.
			allConditions := true
			for _, n := range fieldNames(f) {
				if n != "Conditions" {
					allConditions = false
				}
			}
			if allConditions {
				return
			}
			if hasMarkerInGroup(f, "+listType=") {
				return
			}
			_, line := positionString(fset, f.Pos())
			for _, n := range fieldNames(f) {
				vs = append(vs, Violation{
					Rule:     r.ID(),
					Severity: SeverityWarning,
					File:     path,
					Line:     line,
					Message:  fmt.Sprintf("list field %s.%s has no +listType marker; per K8s API conventions all list fields should declare their list type (atomic, set, or map) for server-side apply correctness", ts.Name.Name, n),
				})
			}
		})
	})
	return vs
}

// ─────────────────────────────────────────────────────────────────────────────
// Rule: no-abbreviations
//
// K8s API conventions: do not use abbreviations in the API except where they
// are extremely commonly used (id, args, stdin).
// ─────────────────────────────────────────────────────────────────────────────

type RuleNoAbbreviations struct{}

func (r *RuleNoAbbreviations) ID() string { return "no-abbreviations" }

// commonAbbreviations are allowed by the K8s conventions document.
var commonAbbreviations = map[string]bool{
	"id":    true,
	"args":  true,
	"stdin": true,
	"url":   true, // ubiquitous
}

// suspectAbbreviations are common bad abbreviations that appear in APIs.
var suspectAbbreviations = map[string]bool{
	"num":   true,
	"cnt":   true,
	"sz":    true,
	"len":   true,
	"addr":  true,
	"src":   true,
	"dst":   true,
	"buf":   true,
	"msg":   true,
	"err":   true,
	"req":   true,
	"resp":  true,
	"cfg":   true,
	"conf":  true,
	"config": false, // not an abbreviation
	"spec":  false,  // not an abbreviation
}

func (r *RuleNoAbbreviations) Check(fset *token.FileSet, file *ast.File, path string) []Violation {
	var vs []Violation
	iterStructTypes(file, func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType) {
		iterFields(st, func(f *ast.Field) {
			if isEmbedded(f) {
				return
			}
			for _, n := range f.Names {
				lower := strings.ToLower(n.Name)
				for abbr, flag := range suspectAbbreviations {
					if !flag {
						continue
					}
					if lower == abbr || strings.HasSuffix(lower, abbr) || strings.HasPrefix(lower, abbr) {
						if commonAbbreviations[abbr] {
							continue
						}
						_, line := positionString(fset, n.Pos())
						vs = append(vs, Violation{
							Rule:     r.ID(),
							Severity: SeverityWarning,
							File:     path,
							Line:     line,
							Message:  fmt.Sprintf("field %s.%s may contain abbreviation %q; per K8s API conventions do not use abbreviations except for id, args, stdin", ts.Name.Name, n.Name, abbr),
						})
						break
					}
				}
			}
		})
	})
	return vs
}
