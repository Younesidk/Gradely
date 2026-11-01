package engine

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	epsilon = 1e-9
	// DefaultNoLabFormula is the default formula for modules without a lab exam.
	DefaultNoLabFormula = "(CC1 + CC2) / 2 * 0.4 + Exam * 0.6"
	// DefaultScale is the grading scale used when nothing else is known.
	DefaultScale = 20.0
)

// Round rounds half away from zero to two decimals.
func Round(v float64) float64 { return math.Round(v*100) / 100 }

// ModuleStatus is where a module stands in its calculation.
type ModuleStatus string

const (
	StatusNoFormula        ModuleStatus = "noFormula"
	StatusFormulaError     ModuleStatus = "formulaError"
	StatusWaitingForGrades ModuleStatus = "waitingForGrades"
	StatusIncomplete       ModuleStatus = "incomplete"
	StatusComplete         ModuleStatus = "complete"
)

// RequiredExamKind classifies the "grade you need in the exam" answer.
type RequiredExamKind string

const (
	ExamNotApplicable RequiredExamKind = "notApplicable"
	ExamNeeded        RequiredExamKind = "needed"
	ExamAlreadySecure RequiredExamKind = "alreadySecured"
	ExamImpossible    RequiredExamKind = "impossible"
	ExamMoreGrades    RequiredExamKind = "moreGradesRequired"
	ExamUnsupported   RequiredExamKind = "unsupported"
)

// ModuleInput is everything the engine needs to know about a module. nil grades are "not entered yet".
type ModuleInput struct {
	CC1, CC2, Lab, Exam *float64
	HasLabExam          bool
	MaxGrade            float64
	PassingThreshold    float64
}

type RequiredExam struct {
	Kind    RequiredExamKind `json:"kind"`
	Value   *float64         `json:"value"`
	Message string           `json:"message"`
}

type ModuleResult struct {
	Status        ModuleStatus `json:"status"`
	Grade         *float64     `json:"grade"`
	Passed        *bool        `json:"passed"`
	StatusText    string       `json:"statusText"`
	MissingInputs []string     `json:"missingInputs"`
	RequiredExam  RequiredExam `json:"requiredExam"`
	FormulaError  *string      `json:"formulaError"`
}

func (r ModuleResult) IsComplete() bool { return r.Status == StatusComplete }

func noExam() RequiredExam { return RequiredExam{Kind: ExamNotApplicable} }

func result(status ModuleStatus, text string, errText *string, missing []string) ModuleResult {
	if missing == nil {
		missing = []string{}
	}
	return ModuleResult{Status: status, StatusText: text, MissingInputs: missing, RequiredExam: noExam(), FormulaError: errText}
}

// Calculate computes a module's result. A missing grade is never treated as zero; an entered 0 is a real grade.
func Calculate(m ModuleInput, formulaExpression string) ModuleResult {
	if strings.TrimSpace(formulaExpression) == "" {
		return result(StatusNoFormula, "No formula selected", nil, nil)
	}
	parsed := Parse(formulaExpression, m.HasLabExam)
	if !parsed.Success() {
		msg := parsed.FirstError()
		return result(StatusFormulaError, "Formula error", &msg, nil)
	}
	formula := parsed.Formula

	values := collectValues(m)
	missing := []string{}
	for _, v := range VariablesFor(m.HasLabExam) {
		if _, ok := values[v]; formula.Variables[v] && !ok {
			missing = append(missing, v)
		}
	}

	if len(values) == 0 && len(formula.Variables) > 0 {
		return result(StatusWaitingForGrades, "Waiting for grades", nil, missing)
	}

	if len(missing) == 0 {
		raw, err := formula.Evaluate(values)
		if err != nil {
			msg := err.Error()
			return result(StatusFormulaError, "Formula error", &msg, nil)
		}
		grade := Round(raw)
		passed := grade+epsilon >= m.PassingThreshold
		text := "Failed"
		if passed {
			text = "Passed"
		}
		return ModuleResult{Status: StatusComplete, Grade: &grade, Passed: &passed, StatusText: text,
			MissingInputs: missing, RequiredExam: noExam()}
	}

	required := noExam()
	if contains(missing, VarExam) {
		if len(missing) == 1 {
			required = SolveRequiredExam(formula, values, m)
		} else {
			var others []string
			for _, x := range missing {
				if x != VarExam {
					others = append(others, x)
				}
			}
			required = RequiredExam{Kind: ExamMoreGrades,
				Message: fmt.Sprintf("Enter %s to calculate the exam grade you need.", strings.Join(others, ", "))}
		}
	}
	return ModuleResult{Status: StatusIncomplete,
		StatusText:    "Incomplete — missing " + strings.Join(missing, ", "),
		MissingInputs: missing, RequiredExam: required}
}

// SolveRequiredExam finds the smallest exam grade (rounded up to 2 decimals) for which the formula reaches
// the passing threshold. It only answers when the formula is provably linear and increasing in Exam.
func SolveRequiredExam(f *Formula, values map[string]float64, m ModuleInput) RequiredExam {
	unsupported := RequiredExam{Kind: ExamUnsupported,
		Message: "This formula's structure doesn't allow a reliable required-exam calculation."}

	a, b, err := f.Linearize(VarExam, values)
	if err != nil {
		return unsupported
	}
	if b <= 1e-12 { // exam has no effect (or lowers the grade): nothing sensible to solve
		return unsupported
	}

	required := (m.PassingThreshold - a) / b
	if required <= epsilon {
		zero := 0.0
		return RequiredExam{Kind: ExamAlreadySecure, Value: &zero,
			Message: fmt.Sprintf("You've already secured the passing threshold (%s/%s) — any exam grade will do.",
				FormatNumber(m.PassingThreshold), FormatNumber(m.MaxGrade))}
	}
	if required > m.MaxGrade+epsilon {
		v := Round(required)
		return RequiredExam{Kind: ExamImpossible, Value: &v,
			Message: fmt.Sprintf("Passing is mathematically impossible under this formula: you would need %s/%s in the exam.",
				FormatNumber(v), FormatNumber(m.MaxGrade))}
	}
	ceiled := math.Ceil(required*100-1e-6) / 100
	return RequiredExam{Kind: ExamNeeded, Value: &ceiled,
		Message: fmt.Sprintf("You need at least %s/%s in your final exam to pass.", FormatNumber(ceiled), FormatNumber(m.MaxGrade))}
}

func collectValues(m ModuleInput) map[string]float64 {
	d := make(map[string]float64, 4)
	if m.CC1 != nil {
		d[VarCC1] = *m.CC1
	}
	if m.CC2 != nil {
		d[VarCC2] = *m.CC2
	}
	if m.HasLabExam && m.Lab != nil {
		d[VarLab] = *m.Lab
	}
	if m.Exam != nil {
		d[VarExam] = *m.Exam
	}
	return d
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// FormatNumber formats a grade with up to two decimals and no trailing zeros ("8", "8.5", "9.67").
func FormatNumber(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

// ---------- semester ----------

// ModuleScore is a module's weight and result, the input of the semester average.
type ModuleScore struct {
	Coefficient float64
	MaxGrade    float64
	Result      ModuleResult
}

// SemesterSummary is the weighted average and status counts for a set of modules.
type SemesterSummary struct {
	Average              *float64 `json:"average"`
	Scale                float64  `json:"scale"`
	ModuleCount          int      `json:"moduleCount"`
	CompletedCount       int      `json:"completedCount"`
	IncompleteCount      int      `json:"incompleteCount"`
	PassedCount          int      `json:"passedCount"`
	FailedCount          int      `json:"failedCount"`
	TotalCoefficient     float64  `json:"totalCoefficient"`
	CompletedCoefficient float64  `json:"completedCoefficient"`
	IsProvisional        bool     `json:"isProvisional"` // the average does not yet cover every module
}

// Summarize computes the weighted semester average. Incomplete modules are excluded, never counted as zero.
func Summarize(modules []ModuleScore) SemesterSummary {
	if len(modules) == 0 {
		return SemesterSummary{Scale: DefaultScale}
	}
	var complete []ModuleScore
	for _, m := range modules {
		if m.Result.IsComplete() && m.Result.Grade != nil {
			complete = append(complete, m)
		}
	}
	passed := 0
	for _, m := range complete {
		if m.Result.Passed != nil && *m.Result.Passed {
			passed++
		}
	}

	// Modules may use different maximum grades; express everything on the most common scale.
	scale := DefaultScale
	if len(complete) > 0 {
		counts := map[float64]int{}
		for _, m := range complete {
			counts[m.MaxGrade]++
		}
		keys := make([]float64, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if counts[keys[i]] != counts[keys[j]] {
				return counts[keys[i]] > counts[keys[j]]
			}
			return keys[i] > keys[j]
		})
		scale = keys[0]
	}

	var total, completedCoef float64
	for _, m := range modules {
		total += m.Coefficient
	}
	for _, m := range complete {
		completedCoef += m.Coefficient
	}

	var average *float64
	if completedCoef > 0 {
		weighted := 0.0
		for _, m := range complete {
			weighted += *m.Result.Grade / m.MaxGrade * scale * m.Coefficient
		}
		avg := Round(weighted / completedCoef)
		average = &avg
	}

	incomplete := len(modules) - len(complete)
	return SemesterSummary{Average: average, Scale: scale, ModuleCount: len(modules), CompletedCount: len(complete),
		IncompleteCount: incomplete, PassedCount: passed, FailedCount: len(complete) - passed,
		TotalCoefficient: total, CompletedCoefficient: completedCoef, IsProvisional: incomplete > 0}
}
