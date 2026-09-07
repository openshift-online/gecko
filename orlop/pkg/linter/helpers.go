package linter

import (
	"go/ast"
	"go/token"
	"strings"
	"unicode"
)

// commentLines returns all comment lines from a comment group as a flat slice.
func commentLines(cg *ast.CommentGroup) []string {
	if cg == nil {
		return nil
	}
	var lines []string
	for _, c := range cg.List {
		lines = append(lines, strings.TrimSpace(c.Text))
	}
	return lines
}

// hasMarker reports whether any comment line in the group contains the given marker string.
func hasMarker(cg *ast.CommentGroup, marker string) bool {
	for _, line := range commentLines(cg) {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// hasMarkerInGroup reports whether the marker appears in either the doc or
// line-comment of a field.
func hasMarkerInGroup(field *ast.Field, marker string) bool {
	return hasMarker(field.Doc, marker) || hasMarker(field.Comment, marker)
}

// fieldDoc returns the combined marker/comment text for a field (doc comment group).
func fieldDoc(field *ast.Field) *ast.CommentGroup {
	return field.Doc
}

// jsonTag returns the JSON struct tag value for a field, or ("", false) if absent.
func jsonTag(field *ast.Field) (string, bool) {
	if field.Tag == nil {
		return "", false
	}
	tag := strings.Trim(field.Tag.Value, "`")
	st := structTagLookup(tag, "json")
	if st == "" {
		return "", false
	}
	return st, true
}

// structTagLookup mimics reflect.StructTag.Lookup but for raw tag strings.
func structTagLookup(tag, key string) string {
	for tag != "" {
		// Skip whitespace
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}

		// Find end of key
		i = 0
		for i < len(tag) && tag[i] != ':' && tag[i] != '"' && tag[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		k := tag[:i]
		tag = tag[i+1:]

		// Find value between quotes
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		qValue := tag[1:i]
		tag = tag[i+1:]

		if k == key {
			return qValue
		}
	}
	return ""
}

// jsonName returns the first element of the json tag (the field name), or "" if absent.
func jsonName(field *ast.Field) string {
	raw, ok := jsonTag(field)
	if !ok {
		return ""
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// hasOmitempty reports whether the json struct tag contains "omitempty".
func hasOmitempty(field *ast.Field) bool {
	raw, ok := jsonTag(field)
	if !ok {
		return false
	}
	for _, opt := range strings.Split(raw, ",")[1:] {
		if opt == "omitempty" {
			return true
		}
	}
	return false
}

// isOptional reports whether a field has +optional comment marker or omitempty tag.
func isOptional(field *ast.Field) bool {
	return hasMarkerInGroup(field, "+optional") || hasOmitempty(field)
}

// isRequired reports whether a field has +required or +kubebuilder:validation:Required marker.
func isRequired(field *ast.Field) bool {
	return hasMarkerInGroup(field, "+required") ||
		hasMarkerInGroup(field, "+kubebuilder:validation:Required")
}

// isPointerType reports whether the AST expression is a pointer (*T).
func isPointerType(expr ast.Expr) bool {
	_, ok := expr.(*ast.StarExpr)
	return ok
}

// isSliceType reports whether the AST expression is a slice ([]T).
func isSliceType(expr ast.Expr) bool {
	arr, ok := expr.(*ast.ArrayType)
	if !ok {
		return false
	}
	return arr.Len == nil // nil Len means it's a slice, not an array
}

// isMapType reports whether the AST expression is a map type.
func isMapType(expr ast.Expr) bool {
	_, ok := expr.(*ast.MapType)
	return ok
}

// isNilable reports whether the type has a built-in nil value (pointer, slice, or map).
func isNilable(expr ast.Expr) bool {
	return isPointerType(expr) || isSliceType(expr) || isMapType(expr)
}

// isEmbedded reports whether the field is an embedded (anonymous) field.
func isEmbedded(field *ast.Field) bool {
	return len(field.Names) == 0
}

// typeName returns a string representation of a type expression, e.g. "int", "string",
// "*Foo", "[]Bar". For qualified names (pkg.Type) it returns "pkg.Type".
func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeName(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeName(t.Elt)
		}
		return "[...]" + typeName(t.Elt)
	case *ast.MapType:
		return "map[" + typeName(t.Key) + "]" + typeName(t.Value)
	case *ast.SelectorExpr:
		return typeName(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return "unknown"
	}
}

// baseTypeName unwraps pointers and slices and returns the innermost type name.
func baseTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return baseTypeName(t.X)
	case *ast.ArrayType:
		return baseTypeName(t.Elt)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

// positionString formats a token.Pos into "file:line".
func positionString(fset *token.FileSet, pos token.Pos) (file string, line int) {
	p := fset.Position(pos)
	return p.Filename, p.Line
}

// isCamelCase reports whether s looks like camelCase (first letter lower, no underscores).
func isCamelCase(s string) bool {
	if s == "" || s == "-" || s == "_" {
		return false
	}
	if unicode.IsUpper(rune(s[0])) {
		return false
	}
	return !strings.ContainsAny(s, "_")
}

// isPascalCase reports whether s looks like PascalCase (first letter upper, no underscores).
func isPascalCase(s string) bool {
	if s == "" {
		return false
	}
	if !unicode.IsUpper(rune(s[0])) {
		return false
	}
	return !strings.ContainsAny(s, "_")
}

// isRootType reports whether a type has +kubebuilder:object:root=true marker.
func isRootType(decl *ast.GenDecl) bool {
	return hasMarker(decl.Doc, "+kubebuilder:object:root=true")
}

// isListType reports whether a type name ends in "List".
func isListKind(name string) bool {
	return strings.HasSuffix(name, "List")
}

// iterStructTypes calls fn for every struct type declaration in the file.
// fn receives the GenDecl (for doc comments), TypeSpec, and StructType.
func iterStructTypes(file *ast.File, fn func(decl *ast.GenDecl, ts *ast.TypeSpec, st *ast.StructType)) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			fn(gd, ts, st)
		}
	}
}

// iterFields calls fn for every named field in a struct.
func iterFields(st *ast.StructType, fn func(field *ast.Field)) {
	if st.Fields == nil {
		return
	}
	for _, f := range st.Fields.List {
		fn(f)
	}
}

// markerValue extracts the value of a marker of the form "// +key=value" or
// "// +key:subkey=value". Returns ("", false) if not found.
func markerValue(cg *ast.CommentGroup, prefix string) (string, bool) {
	for _, line := range commentLines(cg) {
		bare := strings.TrimPrefix(line, "//")
		bare = strings.TrimSpace(bare)
		if strings.HasPrefix(bare, prefix) {
			after := strings.TrimPrefix(bare, prefix)
			return strings.TrimSpace(after), true
		}
	}
	return "", false
}

// hasKubebuilderSubresourceStatus reports whether the type (via its GenDecl doc) has
// "+kubebuilder:subresource:status".
func hasKubebuilderSubresourceStatus(decl *ast.GenDecl) bool {
	return hasMarker(decl.Doc, "+kubebuilder:subresource:status")
}

// fieldNames returns the Go identifier names of a field (may be multiple for `A, B int`).
func fieldNames(f *ast.Field) []string {
	var names []string
	for _, n := range f.Names {
		names = append(names, n.Name)
	}
	return names
}

// isSpecOrStatusField reports whether a named struct field is called "Spec" or "Status".
func isSpecOrStatusField(f *ast.Field) (isSpec, isStatus bool) {
	for _, name := range f.Names {
		switch name.Name {
		case "Spec":
			isSpec = true
		case "Status":
			isStatus = true
		}
	}
	return
}
