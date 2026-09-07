package linter_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/openshift-online/gecko/orlop/pkg/linter"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// checkFile parses src as a Go source file and runs only the supplied rule.
// It returns the slice of violations.
func checkFile(t *testing.T, rule linter.Rule, src string) []linter.Violation {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return rule.Check(fset, f, "test.go")
}

// expectViolation asserts that at least one violation with the given rule ID
// and containing msgSubstr in its message is present.
func expectViolation(t *testing.T, vs []linter.Violation, ruleID, msgSubstr string) {
	t.Helper()
	for _, v := range vs {
		if v.Rule == ruleID && strings.Contains(v.Message, msgSubstr) {
			return
		}
	}
	t.Errorf("expected violation rule=%q containing %q; got violations: %v", ruleID, msgSubstr, vs)
}

// expectNoViolation asserts that NO violation with the given rule ID is present.
func expectNoViolation(t *testing.T, vs []linter.Violation, ruleID string) {
	t.Helper()
	for _, v := range vs {
		if v.Rule == ruleID {
			t.Errorf("unexpected violation rule=%q: %s", ruleID, v.Message)
		}
	}
}

// ─── RuleNoIntFields ──────────────────────────────────────────────────────────

const noIntGood = `package v1
type Foo struct {
	Count int32 ` + "`json:\"count\"`" + `
}`

const noIntBad = `package v1
type Foo struct {
	Count int ` + "`json:\"count\"`" + `
}`

func TestRuleNoIntFields_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoIntFields{}, noIntGood)
	expectNoViolation(t, vs, "no-int-type")
}

func TestRuleNoIntFields_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoIntFields{}, noIntBad)
	expectViolation(t, vs, "no-int-type", "int")
}

// ─── RuleNoUnsignedInts ───────────────────────────────────────────────────────

const noUintGood = `package v1
type Foo struct {
	Count int32 ` + "`json:\"count\"`" + `
}`

const noUintBad = `package v1
type Foo struct {
	Count uint32 ` + "`json:\"count\"`" + `
}`

func TestRuleNoUnsignedInts_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoUnsignedInts{}, noUintGood)
	expectNoViolation(t, vs, "no-unsigned-ints")
}

func TestRuleNoUnsignedInts_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoUnsignedInts{}, noUintBad)
	expectViolation(t, vs, "no-unsigned-ints", "unsigned integer")
}

// ─── RuleNoFloatInSpec ────────────────────────────────────────────────────────

const noFloatGood = `package v1
type FooSpec struct {
	Replicas int32 ` + "`json:\"replicas\"`" + `
}`

const noFloatBad = `package v1
type FooSpec struct {
	Weight float64 ` + "`json:\"weight\"`" + `
}`

func TestRuleNoFloatInSpec_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoFloatInSpec{}, noFloatGood)
	expectNoViolation(t, vs, "no-float-in-spec")
}

func TestRuleNoFloatInSpec_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoFloatInSpec{}, noFloatBad)
	expectViolation(t, vs, "no-float-in-spec", "float")
}

// ─── RuleJSONTagCamelCase ─────────────────────────────────────────────────────

const jsonCamelGood = `package v1
type Foo struct {
	MachineType string ` + "`json:\"machineType,omitempty\"`" + `
}`

const jsonCamelBad = `package v1
type Foo struct {
	MachineType string ` + "`json:\"machine_type,omitempty\"`" + `
}`

const jsonCamelBadUpper = `package v1
type Foo struct {
	MachineType string ` + "`json:\"MachineType,omitempty\"`" + `
}`

func TestRuleJSONTagCamelCase_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleJSONTagCamelCase{}, jsonCamelGood)
	expectNoViolation(t, vs, "json-camelcase")
}

func TestRuleJSONTagCamelCase_Underscore(t *testing.T) {
	vs := checkFile(t, &linter.RuleJSONTagCamelCase{}, jsonCamelBad)
	expectViolation(t, vs, "json-camelcase", "camelCase")
}

func TestRuleJSONTagCamelCase_UpperFirst(t *testing.T) {
	vs := checkFile(t, &linter.RuleJSONTagCamelCase{}, jsonCamelBadUpper)
	expectViolation(t, vs, "json-camelcase", "camelCase")
}

// ─── RuleGoFieldPascalCase ────────────────────────────────────────────────────

const goPascalGood = `package v1
type Foo struct {
	MachineType string ` + "`json:\"machineType\"`" + `
}`

const goPascalBad = `package v1
type Foo struct {
	machineType string ` + "`json:\"machineType\"`" + `
}`

func TestRuleGoFieldPascalCase_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleGoFieldPascalCase{}, goPascalGood)
	expectNoViolation(t, vs, "go-pascalcase")
}

func TestRuleGoFieldPascalCase_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleGoFieldPascalCase{}, goPascalBad)
	expectViolation(t, vs, "go-pascalcase", "PascalCase")
}

// ─── RuleNoIsFooable ──────────────────────────────────────────────────────────

const noIsFooableGood = `package v1
type Foo struct {
	Enabled string ` + "`json:\"enabled\"`" + `
}`

const noIsFooableBad = `package v1
type Foo struct {
	IsEnabled bool ` + "`json:\"isEnabled\"`" + `
}`

func TestRuleNoIsFooable_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoIsFooable{}, noIsFooableGood)
	expectNoViolation(t, vs, "no-is-prefix")
}

func TestRuleNoIsFooable_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoIsFooable{}, noIsFooableBad)
	expectViolation(t, vs, "no-is-prefix", "Is")
}

// ─── RuleTimestampSuffix ──────────────────────────────────────────────────────

const timestampGood = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type Foo struct {
	LastTransitionTime metav1.Time ` + "`json:\"lastTransitionTime\"`" + `
}`

const timestampBad = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type Foo struct {
	CreatedTimestamp metav1.Time ` + "`json:\"createdTimestamp\"`" + `
}`

func TestRuleTimestampSuffix_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleTimestampSuffix{}, timestampGood)
	expectNoViolation(t, vs, "timestamp-suffix")
}

func TestRuleTimestampSuffix_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleTimestampSuffix{}, timestampBad)
	expectViolation(t, vs, "timestamp-suffix", "somethingTime")
}

// ─── RuleNoEnumNumeric ────────────────────────────────────────────────────────

const enumNumericGood = `package v1
type Foo struct {
	// +kubebuilder:validation:Enum=Active;Inactive
	Phase string ` + "`json:\"phase\"`" + `
}`

const enumNumericBad = `package v1
type Foo struct {
	// +kubebuilder:validation:Enum=1;2;3
	Priority int32 ` + "`json:\"priority\"`" + `
}`

func TestRuleNoEnumNumeric_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoEnumNumeric{}, enumNumericGood)
	expectNoViolation(t, vs, "no-enum-numeric")
}

func TestRuleNoEnumNumeric_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoEnumNumeric{}, enumNumericBad)
	expectViolation(t, vs, "no-enum-numeric", "numeric enum")
}

// ─── RuleEnumValuesCamelCase ──────────────────────────────────────────────────

const enumCamelGood = `package v1
type Foo struct {
	// +kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute
	Effect string ` + "`json:\"effect\"`" + `
}`

const enumCamelBad = `package v1
type Foo struct {
	// +kubebuilder:validation:Enum=no_schedule;prefer_no_schedule
	Effect string ` + "`json:\"effect\"`" + `
}`

func TestRuleEnumValuesCamelCase_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleEnumValuesCamelCase{}, enumCamelGood)
	expectNoViolation(t, vs, "enum-camelcase")
}

func TestRuleEnumValuesCamelCase_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleEnumValuesCamelCase{}, enumCamelBad)
	expectViolation(t, vs, "enum-camelcase", "underscore")
}

// ─── RuleSpecStatusTopLevel ───────────────────────────────────────────────────

const topLevelGood = `package v1

// +kubebuilder:object:root=true
type Foo struct {
	TypeMeta   ` + "`json:\",inline\"`" + `
	ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
	Spec   FooSpec   ` + "`json:\"spec,omitempty\"`" + `
	Status FooStatus ` + "`json:\"status,omitempty\"`" + `
}`

const topLevelBad = `package v1

// +kubebuilder:object:root=true
type Foo struct {
	TypeMeta   ` + "`json:\",inline\"`" + `
	ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
	Spec   FooSpec   ` + "`json:\"spec,omitempty\"`" + `
	Status FooStatus ` + "`json:\"status,omitempty\"`" + `
	Extra  string    ` + "`json:\"extra,omitempty\"`" + `
}`

func TestRuleSpecStatusTopLevel_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleSpecStatusTopLevel{}, topLevelGood)
	expectNoViolation(t, vs, "spec-status-top-level")
}

func TestRuleSpecStatusTopLevel_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleSpecStatusTopLevel{}, topLevelBad)
	expectViolation(t, vs, "spec-status-top-level", "Extra")
}

// ─── RuleStatusConditionsListType ─────────────────────────────────────────────

const conditionsListTypeGood = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type FooStatus struct {
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition ` + "`json:\"conditions,omitempty\"`" + `
}`

const conditionsListTypeBad = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type FooStatus struct {
	Conditions []metav1.Condition ` + "`json:\"conditions,omitempty\"`" + `
}`

func TestRuleStatusConditionsListType_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleStatusConditionsListType{}, conditionsListTypeGood)
	expectNoViolation(t, vs, "conditions-list-type")
}

func TestRuleStatusConditionsListType_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleStatusConditionsListType{}, conditionsListTypeBad)
	expectViolation(t, vs, "conditions-list-type", "+listType=map")
}

// ─── RuleConditionsMetav1Type ─────────────────────────────────────────────────

const conditionsMetav1Good = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type FooStatus struct {
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition ` + "`json:\"conditions,omitempty\"`" + `
}`

const conditionsMetav1Bad = `package v1

type FooStatus struct {
	Conditions []string ` + "`json:\"conditions,omitempty\"`" + `
}`

func TestRuleConditionsMetav1Type_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleConditionsMetav1Type{}, conditionsMetav1Good)
	expectNoViolation(t, vs, "conditions-metav1")
}

func TestRuleConditionsMetav1Type_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleConditionsMetav1Type{}, conditionsMetav1Bad)
	expectViolation(t, vs, "conditions-metav1", "metav1.Condition")
}

// ─── RuleNoPhaseField ─────────────────────────────────────────────────────────

const noPhaseGood = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type FooStatus struct {
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition ` + "`json:\"conditions,omitempty\"`" + `
}`

const noPhaseBad = `package v1
type FooStatus struct {
	Phase string ` + "`json:\"phase,omitempty\"`" + `
}`

func TestRuleNoPhaseField_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoPhaseField{}, noPhaseGood)
	expectNoViolation(t, vs, "no-phase")
}

func TestRuleNoPhaseField_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoPhaseField{}, noPhaseBad)
	expectViolation(t, vs, "no-phase", "deprecated")
}

// ─── RuleListKindSuffix ───────────────────────────────────────────────────────

const listKindGood = `package v1
type FooList struct {
	Items []Foo ` + "`json:\"items\"`" + `
}`

const listKindBad = `package v1
type FooCollection struct {
	Items []Foo ` + "`json:\"items\"`" + `
}`

func TestRuleListKindSuffix_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleListKindSuffix{}, listKindGood)
	expectNoViolation(t, vs, "list-kind-suffix")
}

func TestRuleListKindSuffix_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleListKindSuffix{}, listKindBad)
	expectViolation(t, vs, "list-kind-suffix", "List")
}

// ─── RuleObjectMetaEmbedded ───────────────────────────────────────────────────

const objectMetaGood = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
type Foo struct {
	metav1.TypeMeta   ` + "`json:\",inline\"`" + `
	metav1.ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
	Spec FooSpec ` + "`json:\"spec,omitempty\"`" + `
}`

const objectMetaBad = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
type Foo struct {
	metav1.TypeMeta ` + "`json:\",inline\"`" + `
	Spec FooSpec ` + "`json:\"spec,omitempty\"`" + `
}`

func TestRuleObjectMetaEmbedded_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleObjectMetaEmbedded{}, objectMetaGood)
	expectNoViolation(t, vs, "objectmeta-embedded")
}

func TestRuleObjectMetaEmbedded_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleObjectMetaEmbedded{}, objectMetaBad)
	expectViolation(t, vs, "objectmeta-embedded", "ObjectMeta")
}

// ─── RuleTypemetaEmbedded ─────────────────────────────────────────────────────

const typemetaGood = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
type Foo struct {
	metav1.TypeMeta   ` + "`json:\",inline\"`" + `
	metav1.ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
}`

const typemetaBad = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
type Foo struct {
	metav1.ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
}`

func TestRuleTypemetaEmbedded_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleTypemetaEmbedded{}, typemetaGood)
	expectNoViolation(t, vs, "typemeta-embedded")
}

func TestRuleTypemetaEmbedded_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleTypemetaEmbedded{}, typemetaBad)
	expectViolation(t, vs, "typemeta-embedded", "TypeMeta")
}

// ─── RuleSubresourceStatusMarker ─────────────────────────────────────────────

const subresourceGood = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Foo struct {
	metav1.TypeMeta   ` + "`json:\",inline\"`" + `
	metav1.ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
	Status FooStatus  ` + "`json:\"status,omitempty\"`" + `
}`

const subresourceBad = `package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
type Foo struct {
	metav1.TypeMeta   ` + "`json:\",inline\"`" + `
	metav1.ObjectMeta ` + "`json:\"metadata,omitempty\"`" + `
	Status FooStatus  ` + "`json:\"status,omitempty\"`" + `
}`

func TestRuleSubresourceStatusMarker_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleSubresourceStatusMarker{}, subresourceGood)
	expectNoViolation(t, vs, "subresource-status")
}

func TestRuleSubresourceStatusMarker_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleSubresourceStatusMarker{}, subresourceBad)
	expectViolation(t, vs, "subresource-status", "subresource:status")
}

// ─── RuleOptionalRequired ─────────────────────────────────────────────────────

const optReqGood = `package v1
type Foo struct {
	// +required
	Name string ` + "`json:\"name\"`" + `
	// +optional
	Description string ` + "`json:\"description,omitempty\"`" + `
}`

// A field with omitempty implicitly satisfies optional declaration.
const optReqOmitemptyGood = `package v1
type Foo struct {
	Desc string ` + "`json:\"desc,omitempty\"`" + `
}`

const optReqBad = `package v1
type Foo struct {
	Name string ` + "`json:\"name\"`" + `
}`

func TestRuleOptionalRequired_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleOptionalRequired{}, optReqGood)
	expectNoViolation(t, vs, "optional-required-declared")
}

func TestRuleOptionalRequired_OmitemptyGood(t *testing.T) {
	vs := checkFile(t, &linter.RuleOptionalRequired{}, optReqOmitemptyGood)
	expectNoViolation(t, vs, "optional-required-declared")
}

func TestRuleOptionalRequired_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleOptionalRequired{}, optReqBad)
	expectViolation(t, vs, "optional-required-declared", "+optional")
}

// ─── RuleStringFieldsMaxLength ────────────────────────────────────────────────

const stringMaxLenGood = `package v1
type Foo struct {
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Name string ` + "`json:\"name,omitempty\"`" + `
}`

const stringMaxLenEnumGood = `package v1
type Foo struct {
	// +kubebuilder:validation:Enum=Active;Inactive
	// +optional
	State string ` + "`json:\"state,omitempty\"`" + `
}`

const stringMaxLenBad = `package v1
type Foo struct {
	// +optional
	Name string ` + "`json:\"name,omitempty\"`" + `
}`

func TestRuleStringFieldsMaxLength_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleStringFieldsMaxLength{}, stringMaxLenGood)
	expectNoViolation(t, vs, "string-max-length")
}

func TestRuleStringFieldsMaxLength_EnumGood(t *testing.T) {
	vs := checkFile(t, &linter.RuleStringFieldsMaxLength{}, stringMaxLenEnumGood)
	expectNoViolation(t, vs, "string-max-length")
}

func TestRuleStringFieldsMaxLength_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleStringFieldsMaxLength{}, stringMaxLenBad)
	expectViolation(t, vs, "string-max-length", "MaxLength")
}

// ─── RuleListFieldsListType ───────────────────────────────────────────────────

const listTypeGood = `package v1
type Foo struct {
	// +listType=atomic
	// +optional
	Tags []string ` + "`json:\"tags,omitempty\"`" + `
}`

const listTypeBad = `package v1
type Foo struct {
	// +optional
	Tags []string ` + "`json:\"tags,omitempty\"`" + `
}`

func TestRuleListFieldsListType_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleListFieldsListType{}, listTypeGood)
	expectNoViolation(t, vs, "list-list-type")
}

func TestRuleListFieldsListType_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleListFieldsListType{}, listTypeBad)
	expectViolation(t, vs, "list-list-type", "+listType")
}

// ─── RuleDurationSeconds ──────────────────────────────────────────────────────

const durationGood = `package v1
type Foo struct {
	// +optional
	TimeoutSeconds int32 ` + "`json:\"timeoutSeconds,omitempty\"`" + `
}`

const durationBad = `package v1
type Foo struct {
	// +optional
	Timeout int32 ` + "`json:\"timeout,omitempty\"`" + `
}`

func TestRuleDurationSeconds_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleDurationSeconds{}, durationGood)
	expectNoViolation(t, vs, "duration-seconds")
}

func TestRuleDurationSeconds_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleDurationSeconds{}, durationBad)
	expectViolation(t, vs, "duration-seconds", "fooSeconds")
}

// ─── RuleNoBoolFields ─────────────────────────────────────────────────────────

const noBoolGood = `package v1
type Foo struct {
	// +optional
	Enabled *bool ` + "`json:\"enabled,omitempty\"`" + `
}`

const noBoolBad = `package v1
type Foo struct {
	// +optional
	Enabled bool ` + "`json:\"enabled,omitempty\"`" + `
}`

func TestRuleNoBoolFields_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoBoolFields{}, noBoolGood)
	// *bool doesn't trigger the rule (raw bool does).
	expectNoViolation(t, vs, "no-bool-fields")
}

func TestRuleNoBoolFields_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleNoBoolFields{}, noBoolBad)
	expectViolation(t, vs, "no-bool-fields", "bool")
}

// ─── RuleRequiredFieldsNoOmitempty ────────────────────────────────────────────

const reqNoOmitGood = `package v1
type Foo struct {
	// +required
	// +kubebuilder:validation:Required
	Name string ` + "`json:\"name\"`" + `
}`

const reqNoOmitBad = `package v1
type Foo struct {
	// +required
	Name string ` + "`json:\"name,omitempty\"`" + `
}`

func TestRuleRequiredFieldsNoOmitempty_Good(t *testing.T) {
	vs := checkFile(t, &linter.RuleRequiredFieldsNoOmitempty{}, reqNoOmitGood)
	expectNoViolation(t, vs, "required-no-omitempty")
}

func TestRuleRequiredFieldsNoOmitempty_Bad(t *testing.T) {
	vs := checkFile(t, &linter.RuleRequiredFieldsNoOmitempty{}, reqNoOmitBad)
	expectViolation(t, vs, "required-no-omitempty", "omitempty")
}

// ─── Linter.Run integration test ──────────────────────────────────────────────

func TestLinter_Run_CurrentAPITypes(t *testing.T) {
	// Run the full linter over the platform-api private types to ensure it
	// parses real code without crashing. We don't assert zero violations here
	// because existing code may have known deviations; the goal is no panic.
	l := linter.New()
	_, err := l.Run([]string{"../../../platform-api/api/private/v1"})
	if err != nil {
		t.Fatalf("linter.Run returned error: %v", err)
	}
}

func TestLinter_Run_OwnTestTypes(t *testing.T) {
	l := linter.New()
	_, err := l.Run([]string{"../../../orlop/apis/private/test/v1"})
	if err != nil {
		// It's OK if the path doesn't exist in this module's tree — just log.
		t.Logf("path not found (expected in orlop module): %v", err)
	}
}

func TestLinter_FilteredRules(t *testing.T) {
	l := linter.New()
	filtered := l.FilteredRules(map[string]bool{"no-int-type": true, "no-unsigned-ints": true})
	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered rules, got %d", len(filtered))
	}
	for _, r := range filtered {
		if r.ID() != "no-int-type" && r.ID() != "no-unsigned-ints" {
			t.Errorf("unexpected rule %q in filtered set", r.ID())
		}
	}
}
