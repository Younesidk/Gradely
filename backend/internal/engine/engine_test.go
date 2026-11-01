package engine

import (
	"math"
	"sort"
	"strings"
	"testing"
)

var vals = map[string]float64{"CC1": 12, "CC2": 14, "Lab": 16, "Exam": 10}

func eval1(t *testing.T, text string) float64 {
	t.Helper()
	r := Parse(text, true)
	if !r.Success() {
		t.Fatalf("parse %q: %s", text, r.FirstError())
	}
	v, err := r.Formula.Evaluate(vals)
	if err != nil {
		t.Fatalf("evaluate %q: %v", text, err)
	}
	return v
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func f(v float64) *float64 { return &v }

func TestArithmeticAndParentheses(t *testing.T) {
	cases := map[string]float64{
		"1 + 2": 3, "10 - 4 - 3": 3, "2 * 3 + 4": 10, "2 + 3 * 4": 14, "20 / 4 / 5": 1, "(2 + 3) * 4": 20,
		"2 * (3 + (4 - 1))": 12, "-5 + 2": -3, "--5": 5, "+5": 5, "-(2 + 3)": -5, "1.5 * 2": 3, ".5 + .5": 1, "  7   ": 7,
	}
	for text, want := range cases {
		if got := eval1(t, text); !near(got, want) {
			t.Errorf("%q = %v, want %v", text, got, want)
		}
	}
}

func TestDefaultFormulaAndCaseInsensitiveVariables(t *testing.T) {
	if got := eval1(t, DefaultNoLabFormula); !near(got, 13*0.4+10*0.6) {
		t.Errorf("default formula = %v", got)
	}
	if got := eval1(t, "cc1 + CC2 + lab + EXAM"); !near(got, 52) {
		t.Errorf("case-insensitive variables = %v", got)
	}
	r := Parse("cc1 + exam", true)
	var names []string
	for v := range r.Formula.Variables {
		names = append(names, v)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "CC1,Exam" {
		t.Errorf("variables = %v", names)
	}
}

func TestFunctions(t *testing.T) {
	cases := map[string]float64{
		"AVG(CC1, CC2)": 13, "AVG(CC1, CC2, Lab, Exam)": 13, "MIN(CC1, CC2, Lab)": 12, "MAX(CC1, CC2, Lab)": 16,
		"ABS(CC1 - Lab)": 4, "ABS(-3)": 3, "avg(2, 4)": 3, "ROUND(2.567, 2)": 2.57, "ROUND(2.5)": 3,
		"MAX(AVG(CC1, CC2), Exam)": 13,
	}
	for text, want := range cases {
		if got := eval1(t, text); !near(got, want) {
			t.Errorf("%q = %v, want %v", text, got, want)
		}
	}
}

func TestInvalidSyntaxIsReportedNotPanicked(t *testing.T) {
	for _, text := range []string{"", "   ", "1 +", "* 2", "(1 + 2", "1 + 2)", "1 2", "CC1 CC2", "2CC1", "1..2", "AVG(",
		"AVG(1,)", "()", "1 + $", "1 ^ 2", "CC1 = 2", "1e5"} {
		r := Parse(text, true)
		if r.Success() || len(r.Errors) == 0 || strings.TrimSpace(r.FirstError()) == "" {
			t.Errorf("%q should be rejected with a message", text)
		}
	}
}

func TestUnknownNamesAndBadArity(t *testing.T) {
	cases := map[string]string{
		"Foo + 1": "Unknown variable", "SQRT(4)": "Unknown function", "ABS(1, 2)": "exactly 1",
		"AVG()": "or more", "ROUND(1, 2, 3)": "1 to 2",
	}
	for text, part := range cases {
		r := Parse(text, true)
		if r.Success() || !strings.Contains(r.FirstError(), part) {
			t.Errorf("%q: error %q should contain %q", text, r.FirstError(), part)
		}
	}
}

func TestLabVariableRejectedWhenNotAllowed(t *testing.T) {
	r := Parse("CC1 + Lab", false)
	if r.Success() || !strings.Contains(r.FirstError(), "Lab") {
		t.Errorf("Lab should be rejected: %q", r.FirstError())
	}
	if !Parse("CC1 + Lab", true).Success() {
		t.Error("Lab should be allowed")
	}
}

func TestEvaluationErrors(t *testing.T) {
	div := Parse("CC1 / (CC2 - 14)", true).Formula
	if _, err := div.Evaluate(vals); err == nil || !strings.Contains(err.Error(), "zero") {
		t.Errorf("division by zero: %v", err)
	}
	missing := Parse("CC1 + Exam", true).Formula
	if _, err := missing.Evaluate(map[string]float64{"CC1": 5}); err == nil || !strings.Contains(err.Error(), "Exam") {
		t.Errorf("missing value: %v", err)
	}
	big := strings.Repeat("9", 200)
	huge := Parse(big+" * "+big, true).Formula
	if _, err := huge.Evaluate(vals); err == nil || !strings.Contains(err.Error(), "finite") {
		t.Errorf("non-finite: %v", err)
	}
}

func TestMaliciousInputIsRejected(t *testing.T) {
	for _, text := range []string{
		`System.IO.File.ReadAllText("/etc/passwd")`, `Environment.GetEnvironmentVariable("ADMIN_PASSWORD")`,
		"1; DROP TABLE Users", "CC1; Exam", "${jndi:ldap://x}", "<script>alert(1)</script>", "eval('1+1')",
		"CC1 + `ls`", "new Object()", `"; var x = 1; //`, "__proto__", `CC1  Exam`, "typeof(CC1)", "Exam.ToString()",
	} {
		if r := Parse(text, true); r.Success() || r.Formula != nil {
			t.Errorf("%q should be rejected", text)
		}
	}
}

func TestDeepNestingAndLength(t *testing.T) {
	nested := strings.Repeat("(", 220) + "1" + strings.Repeat(")", 220)
	if r := Parse(nested, true); r.Success() || !strings.Contains(r.FirstError(), "nested") {
		t.Errorf("deep nesting: %q", r.FirstError())
	}
	if Parse(strings.Repeat("-", 450)+"1", true).Success() {
		t.Error("450 unary minuses should be rejected")
	}
	if Parse(strings.Repeat("ABS(", 100)+"1"+strings.Repeat(")", 100), true).Success() {
		t.Error("100 nested calls should be rejected")
	}
	long := strings.Repeat("1 + ", 399) + "1"
	if r := Parse(long, true); r.Success() || !strings.Contains(r.FirstError(), "too long") {
		t.Errorf("long input: %q", r.FirstError())
	}
	if got := eval1(t, strings.Repeat("(", 30)+"1"+strings.Repeat(")", 30)); got != 1 {
		t.Errorf("moderate nesting = %v", got)
	}
}

func TestLinearize(t *testing.T) {
	fm := Parse("(CC1 + CC2) / 2 * 0.4 + Exam * 0.6", true).Formula
	a, b, err := fm.Linearize("Exam", map[string]float64{"CC1": 12, "CC2": 14})
	if err != nil || !near(a, 5.2) || !near(b, 0.6) {
		t.Errorf("linearize = %v, %v, %v", a, b, err)
	}
	for _, text := range []string{"Exam * Exam", "10 / Exam", "MAX(Exam, 10)", "ABS(Exam - 5)", "ROUND(Exam)"} {
		fm := Parse(text, true).Formula
		if _, _, err := fm.Linearize("Exam", map[string]float64{"CC1": 1}); !IsNotLinear(err) {
			t.Errorf("%q should be reported as non-linear, got %v", text, err)
		}
	}
}

// ---------- grade calculator ----------

const labFormula = "(CC1 + CC2) / 2 * 0.2 + Lab * 0.2 + Exam * 0.6" // test-only weighting

func mod(cc1, cc2, lab, exam *float64, hasLab bool, maxGrade, pass float64) ModuleInput {
	return ModuleInput{CC1: cc1, CC2: cc2, Lab: lab, Exam: exam, HasLabExam: hasLab, MaxGrade: maxGrade, PassingThreshold: pass}
}

func plain(cc1, cc2, exam *float64) ModuleInput { return mod(cc1, cc2, nil, exam, false, 20, 10) }

func TestNoGradesWaits(t *testing.T) {
	r := Calculate(plain(nil, nil, nil), DefaultNoLabFormula)
	if r.Status != StatusWaitingForGrades || r.StatusText != "Waiting for grades" || r.Grade != nil || r.Passed != nil {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestDefaultFormulaFullGrades(t *testing.T) {
	r := Calculate(plain(f(12), f(14), f(13)), DefaultNoLabFormula)
	if r.Status != StatusComplete || !near(*r.Grade, 13) || !*r.Passed || r.StatusText != "Passed" {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRequiredExamSpecExample(t *testing.T) {
	r := Calculate(plain(f(12), f(14), nil), DefaultNoLabFormula)
	if r.Status != StatusIncomplete || r.RequiredExam.Kind != ExamNeeded || *r.RequiredExam.Value != 8 ||
		r.RequiredExam.Message != "You need at least 8/20 in your final exam to pass." ||
		r.Grade != nil || len(r.MissingInputs) != 1 || r.MissingInputs[0] != "Exam" {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRequiredExamRoundedUp(t *testing.T) {
	r := Calculate(plain(f(10), f(11), nil), DefaultNoLabFormula)
	if *r.RequiredExam.Value != 9.67 {
		t.Errorf("required = %v", *r.RequiredExam.Value)
	}
	if !*Calculate(plain(f(10), f(11), f(9.67)), DefaultNoLabFormula).Passed {
		t.Error("9.67 should pass")
	}
	if *Calculate(plain(f(10), f(11), f(9.65)), DefaultNoLabFormula).Passed {
		t.Error("9.65 should fail")
	}
}

func TestAlreadySecuredAndImpossible(t *testing.T) {
	r := Calculate(plain(f(20), f(20), nil), "(CC1 + CC2) / 2 * 0.8 + Exam * 0.2")
	if r.RequiredExam.Kind != ExamAlreadySecure || !strings.Contains(r.RequiredExam.Message, "already secured") || r.Status != StatusIncomplete {
		t.Errorf("already secured: %+v", r)
	}
	possible := Calculate(plain(f(0), f(0), nil), DefaultNoLabFormula)
	if possible.RequiredExam.Kind != ExamNeeded || *possible.RequiredExam.Value != 16.67 {
		t.Errorf("possible: %+v", possible.RequiredExam)
	}
	impossible := Calculate(plain(f(0), f(0), nil), "(CC1 + CC2) / 2 * 0.8 + Exam * 0.2")
	if impossible.RequiredExam.Kind != ExamImpossible || !strings.Contains(impossible.RequiredExam.Message, "impossible") {
		t.Errorf("impossible: %+v", impossible.RequiredExam)
	}
	exact := Calculate(plain(f(0), f(0), nil), "(CC1 + CC2) / 2 * 0.5 + Exam * 0.5")
	if exact.RequiredExam.Kind != ExamNeeded || *exact.RequiredExam.Value != 20 {
		t.Errorf("exactly max: %+v", exact.RequiredExam)
	}
}

func TestZeroIsAGradeButNilIsMissing(t *testing.T) {
	r := Calculate(plain(f(0), f(0), f(0)), DefaultNoLabFormula)
	if r.Status != StatusComplete || *r.Grade != 0 || *r.Passed || r.StatusText != "Failed" {
		t.Errorf("all zero: %+v", r)
	}
	partial := Calculate(plain(f(0), nil, nil), DefaultNoLabFormula)
	if partial.Status != StatusIncomplete || strings.Join(partial.MissingInputs, ",") != "CC2,Exam" {
		t.Errorf("partial: %+v", partial)
	}
	if Calculate(plain(nil, nil, f(0)), DefaultNoLabFormula).Status != StatusIncomplete {
		t.Error("only a zero entered should be incomplete, not waiting")
	}
}

func TestMissingCourseworkRequiresMoreGrades(t *testing.T) {
	r := Calculate(plain(f(12), nil, nil), DefaultNoLabFormula)
	if r.Status != StatusIncomplete || r.RequiredExam.Kind != ExamMoreGrades || !strings.Contains(r.RequiredExam.Message, "CC2") || r.RequiredExam.Value != nil {
		t.Errorf("unexpected: %+v", r)
	}
	examOnly := Calculate(plain(nil, nil, f(15)), DefaultNoLabFormula)
	if examOnly.Status != StatusIncomplete || examOnly.Grade != nil || examOnly.RequiredExam.Kind != ExamNotApplicable {
		t.Errorf("exam only: %+v", examOnly)
	}
}

func TestLabModules(t *testing.T) {
	missingLab := Calculate(mod(f(12), f(14), nil, f(13), true, 20, 10), labFormula)
	if missingLab.Status != StatusIncomplete || !contains(missingLab.MissingInputs, "Lab") || missingLab.Grade != nil {
		t.Errorf("missing lab: %+v", missingLab)
	}
	full := Calculate(mod(f(12), f(14), f(16), f(10), true, 20, 10), labFormula)
	if full.Status != StatusComplete || !near(*full.Grade, 13*0.2+16*0.2+10*0.6) {
		t.Errorf("full: %+v", full)
	}
	need := Calculate(mod(f(12), f(14), f(16), nil, true, 20, 10), labFormula)
	if need.RequiredExam.Kind != ExamNeeded || *need.RequiredExam.Value != 7 {
		t.Errorf("need: %+v", need.RequiredExam)
	}
	more := Calculate(mod(f(12), f(14), nil, nil, true, 20, 10), labFormula)
	if more.RequiredExam.Kind != ExamMoreGrades || !strings.Contains(more.RequiredExam.Message, "Lab") {
		t.Errorf("more: %+v", more.RequiredExam)
	}
	ignored := Calculate(mod(f(12), f(14), f(3), f(13), false, 20, 10), DefaultNoLabFormula)
	if *ignored.Grade != 13 {
		t.Errorf("lab should be ignored without lab exam: %v", *ignored.Grade)
	}
	wrongKind := Calculate(plain(f(12), f(14), f(13)), labFormula)
	if wrongKind.Status != StatusFormulaError || !strings.Contains(*wrongKind.FormulaError, "Lab") {
		t.Errorf("wrong kind: %+v", wrongKind)
	}
}

func TestMissingOrInvalidFormula(t *testing.T) {
	in := plain(f(1), f(2), f(3))
	if Calculate(in, "").Status != StatusNoFormula || Calculate(in, "  ").Status != StatusNoFormula {
		t.Error("empty formula should be noFormula")
	}
	if Calculate(in, "CC1 +").Status != StatusFormulaError {
		t.Error("bad syntax should be formulaError")
	}
	div := Calculate(plain(f(12), f(14), f(13)), "Exam / (CC1 - 12)")
	if div.Status != StatusFormulaError || !strings.Contains(*div.FormulaError, "zero") {
		t.Errorf("division: %+v", div)
	}
}

func TestUnsupportedStructuresDoNotFabricateRequiredExam(t *testing.T) {
	for _, expr := range []string{"MAX(CC1, Exam)", "Exam * Exam", "10 / Exam", "AVG(CC1, CC2, Exam) - Exam / 3 + 0 * Exam", "CC1 - Exam"} {
		r := Calculate(plain(f(12), f(14), nil), expr)
		if r.Status != StatusIncomplete || r.RequiredExam.Kind != ExamUnsupported || r.RequiredExam.Value != nil {
			t.Errorf("%q: %+v", expr, r)
		}
	}
}

func TestCustomThresholdAndScale(t *testing.T) {
	r := Calculate(mod(f(60), f(80), nil, nil, false, 100, 50), DefaultNoLabFormula)
	if *r.RequiredExam.Value != 36.67 || !strings.Contains(r.RequiredExam.Message, "/100") {
		t.Errorf("unexpected: %+v", r.RequiredExam)
	}
}

func TestRoundingAndPassUsesRoundedGrade(t *testing.T) {
	fail := Calculate(plain(f(10), f(10), f(9.99)), DefaultNoLabFormula)
	if *fail.Grade != 9.99 || *fail.Passed {
		t.Errorf("fail: %+v", fail)
	}
	pass := Calculate(plain(f(10), f(10), f(10.01)), DefaultNoLabFormula)
	if *pass.Grade != 10.01 || !*pass.Passed {
		t.Errorf("pass: %+v", pass)
	}
	edge := Calculate(plain(f(10), f(10), f(9.992)), DefaultNoLabFormula)
	if *edge.Grade != 10.0 || !*edge.Passed {
		t.Errorf("edge: %+v", edge)
	}
	dec := Calculate(plain(f(11.5), f(12.25), f(13.75)), DefaultNoLabFormula)
	if *dec.Grade != Round(11.875*0.4+13.75*0.6) {
		t.Errorf("decimals: %v", *dec.Grade)
	}
}

func TestNumberFormatting(t *testing.T) {
	for in, want := range map[float64]string{8: "8", 8.5: "8.5", 9.666: "9.67", 0: "0", 12.5: "12.5"} {
		if got := FormatNumber(in); got != want {
			t.Errorf("FormatNumber(%v) = %q, want %q", in, got, want)
		}
	}
}

// ---------- semester ----------

func score(coeff float64, cc1, cc2, exam *float64, max, pass float64) ModuleScore {
	return ModuleScore{coeff, max, Calculate(mod(cc1, cc2, nil, exam, false, max, pass), DefaultNoLabFormula)}
}

func TestEmptySemester(t *testing.T) {
	s := Summarize(nil)
	if s.Average != nil || s.ModuleCount != 0 || s.IsProvisional || s.Scale != 20 {
		t.Errorf("unexpected: %+v", s)
	}
}

func TestWeightedAverage(t *testing.T) {
	s := Summarize([]ModuleScore{score(3, f(12), f(14), f(13), 20, 10), score(1, f(8), f(8), f(8), 20, 10)})
	if !near(*s.Average, (13*3+8)/4.0) || s.CompletedCount != 2 || s.PassedCount != 1 || s.FailedCount != 1 || s.IsProvisional {
		t.Errorf("unexpected: %+v", s)
	}
	plainMean := Summarize([]ModuleScore{score(1, f(10), f(10), f(10), 20, 10), score(1, f(16), f(16), f(16), 20, 10)})
	if *plainMean.Average != 13 {
		t.Errorf("plain mean = %v", *plainMean.Average)
	}
}

func TestIncompleteModulesAreExcluded(t *testing.T) {
	s := Summarize([]ModuleScore{
		score(2, f(12), f(14), f(13), 20, 10), score(5, f(10), f(10), nil, 20, 10), score(5, nil, nil, nil, 20, 10),
	})
	if *s.Average != 13 || s.ModuleCount != 3 || s.CompletedCount != 1 || s.IncompleteCount != 2 || !s.IsProvisional ||
		s.TotalCoefficient != 12 || s.CompletedCoefficient != 2 {
		t.Errorf("unexpected: %+v", s)
	}
	none := Summarize([]ModuleScore{score(1, nil, nil, nil, 20, 10), score(1, f(10), nil, nil, 20, 10)})
	if none.Average != nil || none.IncompleteCount != 2 || !none.IsProvisional {
		t.Errorf("none complete: %+v", none)
	}
}

func TestZeroGradesAndZeroCoefficient(t *testing.T) {
	s := Summarize([]ModuleScore{score(1, f(0), f(0), f(0), 20, 10), score(1, f(20), f(20), f(20), 20, 10)})
	if *s.Average != 10 || s.FailedCount != 1 {
		t.Errorf("zero grade: %+v", s)
	}
	z := Summarize([]ModuleScore{score(0, f(12), f(14), f(13), 20, 10)})
	if z.Average != nil || z.CompletedCount != 1 {
		t.Errorf("zero coefficient: %+v", z)
	}
}

func TestMixedScalesNormalise(t *testing.T) {
	s := Summarize([]ModuleScore{
		score(1, f(12), f(14), f(13), 20, 10), score(1, f(10), f(10), f(10), 20, 10), score(1, f(50), f(50), f(50), 100, 50),
	})
	if s.Scale != 20 || *s.Average != Round((13+10+10)/3.0) {
		t.Errorf("unexpected: %+v", s)
	}
	third := Summarize([]ModuleScore{score(1, f(10), f(10), f(10), 20, 10), score(1, f(10), f(10), f(11), 20, 10), score(1, f(10), f(10), f(11), 20, 10)})
	if *third.Average != Round((10+10.6+10.6)/3) {
		t.Errorf("rounding: %v", *third.Average)
	}
}
