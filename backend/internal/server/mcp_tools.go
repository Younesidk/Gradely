package server

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/younesidk/gradely/internal/engine"
	"github.com/younesidk/gradely/internal/models"
)

// The MCP tools. Each one runs as the authenticated principal's user and goes through the same service functions,
// validation and grading engine as the web app; none of them touches the database on its own terms.

// ---------- output shapes ----------

type semesterOut struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	AcademicYear *string   `json:"academic_year"`
	StartDate    *string   `json:"start_date" jsonschema:"YYYY-MM-DD, or null when not set"`
	EndDate      *string   `json:"end_date" jsonschema:"YYYY-MM-DD, or null when not set"`
	Position     int       `json:"position" jsonschema:"display order, starting at 0"`
	ModuleCount  int       `json:"module_count"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type moduleOut struct {
	ID               string    `json:"id"`
	SemesterID       string    `json:"semester_id"`
	Name             string    `json:"name"`
	Code             *string   `json:"code"`
	Coefficient      float64   `json:"coefficient" jsonschema:"weight of the module in the semester average"`
	MaxGrade         float64   `json:"max_grade" jsonschema:"top of the grading scale, e.g. 20"`
	PassingThreshold float64   `json:"passing_threshold" jsonschema:"module grade needed to pass"`
	HasLabExam       bool      `json:"has_lab_exam" jsonschema:"whether the module has a Lab assessment"`
	FormulaID        *string   `json:"formula_id"`
	FormulaName      *string   `json:"formula_name"`
	Position         int       `json:"position"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type gradeOut struct {
	Assessment string   `json:"assessment" jsonschema:"CC1, CC2, Lab or Exam"`
	Value      *float64 `json:"value" jsonschema:"the grade, or null when not entered yet (never treated as zero)"`
	Entered    bool     `json:"entered"`
}

type requiredExamOut struct {
	Kind    string   `json:"kind" jsonschema:"needed, alreadySecured, impossible, moreGradesRequired, unsupported or notApplicable"`
	Value   *float64 `json:"value"`
	Message string   `json:"message"`
}

type moduleResultOut struct {
	ModuleID           string          `json:"module_id"`
	ModuleName         string          `json:"module_name"`
	Status             string          `json:"status" jsonschema:"complete, incomplete, waitingForGrades, noFormula or formulaError"`
	StatusText         string          `json:"status_text"`
	Grade              *float64        `json:"grade" jsonschema:"calculated module grade; null unless every assessment the formula needs is entered"`
	Passed             *bool           `json:"passed"`
	MaxGrade           float64         `json:"max_grade"`
	PassingThreshold   float64         `json:"passing_threshold"`
	Coefficient        float64         `json:"coefficient"`
	MissingAssessments []string        `json:"missing_assessments"`
	RequiredExam       requiredExamOut `json:"required_exam"`
	FormulaName        *string         `json:"formula_name"`
	FormulaError       *string         `json:"formula_error"`
}

type summaryOut struct {
	Average              *float64 `json:"average" jsonschema:"coefficient-weighted average of complete modules, or null when none is complete"`
	Scale                float64  `json:"scale" jsonschema:"scale the average is expressed on"`
	IsProvisional        bool     `json:"is_provisional" jsonschema:"true when some modules are not complete yet and are excluded from the average"`
	ModuleCount          int      `json:"module_count"`
	CompletedCount       int      `json:"completed_count"`
	IncompleteCount      int      `json:"incomplete_count"`
	PassedCount          int      `json:"passed_count"`
	FailedCount          int      `json:"failed_count"`
	TotalCoefficient     float64  `json:"total_coefficient"`
	CompletedCoefficient float64  `json:"completed_coefficient"`
}

type formulaOut struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Expression   string   `json:"expression"`
	ForLabExam   bool     `json:"for_lab_exam" jsonschema:"true for modules with a lab exam, false for modules without"`
	IsDefault    bool     `json:"is_default" jsonschema:"used for new modules of its kind when no formula is chosen"`
	ModulesUsing int      `json:"modules_using"`
	Variables    []string `json:"variables" jsonschema:"assessments the formula uses"`
}

func toSemesterOut(s semesterDTO, moduleCount int) semesterOut {
	return semesterOut{s.ID, s.Name, s.AcademicYear, s.StartDate, s.EndDate, s.SortOrder, moduleCount, s.UpdatedAt}
}

func toModuleOut(v moduleView) moduleOut {
	m := v.Module
	var name *string
	if v.Formula != nil {
		name = &v.Formula.Name
	}
	return moduleOut{m.ID, m.SemesterID, m.Name, m.Code, m.Coefficient, m.MaxGrade, m.PassingThreshold, m.HasLabExam,
		m.FormulaID, name, m.SortOrder, m.UpdatedAt}
}

func toResultOut(v moduleView) moduleResultOut {
	r := v.Result
	var name *string
	if v.Formula != nil {
		name = &v.Formula.Name
	}
	missing := r.MissingInputs
	if missing == nil {
		missing = []string{}
	}
	return moduleResultOut{v.Module.ID, v.Module.Name, string(r.Status), r.StatusText, r.Grade, r.Passed,
		v.Module.MaxGrade, v.Module.PassingThreshold, v.Module.Coefficient, missing,
		requiredExamOut{string(r.RequiredExam.Kind), r.RequiredExam.Value, r.RequiredExam.Message}, name, r.FormulaError}
}

func toSummaryOut(s engine.SemesterSummary) summaryOut {
	return summaryOut{s.Average, s.Scale, s.IsProvisional, s.ModuleCount, s.CompletedCount, s.IncompleteCount,
		s.PassedCount, s.FailedCount, s.TotalCoefficient, s.CompletedCoefficient}
}

func formulaVariables(expression string, forLab bool) []string {
	vars := []string{}
	parsed := engine.Parse(expression, forLab)
	if !parsed.Success() {
		return vars
	}
	for _, v := range engine.AllVariables {
		if parsed.Formula.Variables[v] {
			vars = append(vars, v)
		}
	}
	return vars
}

func toFormulaOut(f models.Formula, using int) formulaOut {
	return formulaOut{f.ID, f.Name, f.Expression, f.ForLabExam, f.IsDefault, using, formulaVariables(f.Expression, f.ForLabExam)}
}

// ---------- assessments ----------

var assessmentEnum = []any{engine.VarCC1, engine.VarCC2, engine.VarLab, engine.VarExam}

func canonicalAssessment(s string) (string, error) {
	for _, v := range engine.AllVariables {
		if strings.EqualFold(strings.TrimSpace(s), v) {
			return v, nil
		}
	}
	return "", &toolError{"validation", "assessment must be one of CC1, CC2, Lab, Exam."}
}

func gradeOf(m *models.Module, assessment string) *float64 {
	switch assessment {
	case engine.VarCC1:
		return m.CC1
	case engine.VarCC2:
		return m.CC2
	case engine.VarLab:
		return m.LabGrade
	default:
		return m.ExamGrade
	}
}

func gradesOf(m *models.Module) gradesForm {
	return gradesForm{CC1: m.CC1, CC2: m.CC2, Lab: m.LabGrade, Exam: m.ExamGrade}
}

func withGrade(g gradesForm, assessment string, v *float64) gradesForm {
	switch assessment {
	case engine.VarCC1:
		g.CC1 = v
	case engine.VarCC2:
		g.CC2 = v
	case engine.VarLab:
		g.Lab = v
	default:
		g.Exam = v
	}
	return g
}

func gradeList(cc1, cc2, lab, exam *float64, hasLab bool) []gradeOut {
	out := []gradeOut{{engine.VarCC1, cc1, cc1 != nil}, {engine.VarCC2, cc2, cc2 != nil}}
	if hasLab {
		out = append(out, gradeOut{engine.VarLab, lab, lab != nil})
	}
	return append(out, gradeOut{engine.VarExam, exam, exam != nil})
}

// ---------- loading ----------

// views loads the user's semesters (or one) with every module's calculated result, in three queries.
func (a *App) views(userID, semesterID string) ([]semesterView, error) {
	list, err := a.loadSemesters(userID, semesterID)
	if err != nil {
		return nil, err
	}
	if semesterID != "" && len(list) == 0 {
		return nil, notFound("That semester")
	}
	return list, nil
}

func (a *App) moduleView(userID, moduleID string) (*moduleView, *semesterView, error) {
	m, err := a.ownedModule(a.DB, userID, moduleID)
	if err != nil {
		return nil, nil, err
	}
	list, err := a.views(userID, m.SemesterID)
	if err != nil {
		return nil, nil, err
	}
	for i := range list[0].Modules {
		if list[0].Modules[i].Module.ID == moduleID {
			return &list[0].Modules[i], &list[0], nil
		}
	}
	return nil, nil, notFound("That module")
}

func requireConfirm(confirm bool, what string) error {
	if !confirm {
		return &toolError{"validation", "Deleting " + what + " can't be undone. Ask the user to confirm, then call again with confirm=true."}
	}
	return nil
}

// optionalText applies a partial update to an optional text field: nil keeps it, "" clears it.
func optionalText(current *string, in *string) *string {
	if in == nil {
		return current
	}
	return clean(in)
}

// ---------- inputs ----------

type noInput struct{}

type semesterIDInput struct {
	SemesterID string `json:"semester_id" jsonschema:"id of the semester (from list_semesters)"`
}

type optionalSemesterInput struct {
	SemesterID string `json:"semester_id,omitempty" jsonschema:"limit to this semester; omit for all semesters"`
}

type moduleIDInput struct {
	ModuleID string `json:"module_id" jsonschema:"id of the module (from list_modules)"`
}

type formulaIDInput struct {
	FormulaID string `json:"formula_id" jsonschema:"id of the formula (from list_formulas)"`
}

type createSemesterInput struct {
	Name           string  `json:"name" jsonschema:"semester name, e.g. 'Semester 3' (max 100 characters)"`
	AcademicYear   *string `json:"academic_year,omitempty" jsonschema:"e.g. '2025-2026' (max 50 characters)"`
	StartDate      *string `json:"start_date,omitempty" jsonschema:"YYYY-MM-DD"`
	EndDate        *string `json:"end_date,omitempty" jsonschema:"YYYY-MM-DD, not before start_date"`
	IdempotencyKey string  `json:"idempotency_key,omitempty" jsonschema:"optional unique key; retrying with the same key returns the first result instead of creating a duplicate"`
}

type updateSemesterInput struct {
	SemesterID   string  `json:"semester_id" jsonschema:"id of the semester to change"`
	Name         *string `json:"name,omitempty" jsonschema:"new name; omit to keep"`
	AcademicYear *string `json:"academic_year,omitempty" jsonschema:"new academic year; omit to keep, empty string to clear"`
	StartDate    *string `json:"start_date,omitempty" jsonschema:"YYYY-MM-DD; omit to keep, empty string to clear"`
	EndDate      *string `json:"end_date,omitempty" jsonschema:"YYYY-MM-DD; omit to keep, empty string to clear"`
}

type deleteSemesterInput struct {
	SemesterID string `json:"semester_id" jsonschema:"id of the semester to delete"`
	Confirm    bool   `json:"confirm,omitempty" jsonschema:"must be true to delete; only set it after the user confirmed the deletion"`
}

type createModuleInput struct {
	SemesterID       string   `json:"semester_id" jsonschema:"semester the module belongs to"`
	Name             string   `json:"name" jsonschema:"module name (max 100 characters)"`
	Code             *string  `json:"code,omitempty" jsonschema:"optional module code (max 30 characters)"`
	Coefficient      *float64 `json:"coefficient,omitempty" jsonschema:"weight in the semester average, 0-1000 (default 1)"`
	MaxGrade         *float64 `json:"max_grade,omitempty" jsonschema:"top of the grading scale, greater than 0 (default 20)"`
	PassingThreshold *float64 `json:"passing_threshold,omitempty" jsonschema:"grade needed to pass, between 0 and max_grade (default 10)"`
	HasLabExam       bool     `json:"has_lab_exam,omitempty" jsonschema:"true if the module has a Lab assessment (default false)"`
	FormulaID        *string  `json:"formula_id,omitempty" jsonschema:"formula to use; it must match has_lab_exam. Omit to use the default formula for that kind"`
	IdempotencyKey   string   `json:"idempotency_key,omitempty" jsonschema:"optional unique key; retrying with the same key returns the first result instead of creating a duplicate"`
}

type updateModuleInput struct {
	ModuleID         string   `json:"module_id" jsonschema:"id of the module to change"`
	Name             *string  `json:"name,omitempty" jsonschema:"omit to keep"`
	Code             *string  `json:"code,omitempty" jsonschema:"omit to keep, empty string to clear"`
	Coefficient      *float64 `json:"coefficient,omitempty" jsonschema:"omit to keep"`
	MaxGrade         *float64 `json:"max_grade,omitempty" jsonschema:"omit to keep; existing grades must still fit"`
	PassingThreshold *float64 `json:"passing_threshold,omitempty" jsonschema:"omit to keep"`
	HasLabExam       *bool    `json:"has_lab_exam,omitempty" jsonschema:"omit to keep. Changing it switches to the default formula of the new kind unless formula_id is given"`
	FormulaID        *string  `json:"formula_id,omitempty" jsonschema:"omit to keep, empty string for the default formula"`
}

type deleteModuleInput struct {
	ModuleID string `json:"module_id" jsonschema:"id of the module to delete"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"must be true to delete; only set it after the user confirmed the deletion"`
}

type setModuleFormulaInput struct {
	ModuleID  string `json:"module_id" jsonschema:"id of the module"`
	FormulaID string `json:"formula_id,omitempty" jsonschema:"formula to use (must match the module's lab/no-lab kind); omit or empty for the default"`
}

type gradeInput struct {
	ModuleID   string `json:"module_id" jsonschema:"id of the module"`
	Assessment string `json:"assessment" jsonschema:"exactly CC1, CC2, Lab (modules with a lab exam only) or Exam"`
}

type gradeValueInput struct {
	ModuleID   string  `json:"module_id" jsonschema:"id of the module"`
	Assessment string  `json:"assessment" jsonschema:"exactly CC1, CC2, Lab (modules with a lab exam only) or Exam"`
	Value      float64 `json:"value" jsonschema:"the grade, between 0 and the module's max_grade; 0 is a real zero"`
}

type listFormulasInput struct {
	ForLabExam *bool `json:"for_lab_exam,omitempty" jsonschema:"true for lab formulas only, false for no-lab formulas only; omit for all"`
}

type createFormulaInput struct {
	Name           string `json:"name" jsonschema:"formula name, unique per user (max 80 characters)"`
	Expression     string `json:"expression" jsonschema:"e.g. '(CC1 + CC2) / 2 * 0.4 + Exam * 0.6'. Variables CC1, CC2, Lab (lab formulas only), Exam"`
	ForLabExam     bool   `json:"for_lab_exam,omitempty" jsonschema:"true if the formula is for modules with a lab exam (default false)"`
	MakeDefault    bool   `json:"make_default,omitempty" jsonschema:"make it the default formula for its kind"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"optional unique key; retrying with the same key returns the first result instead of creating a duplicate"`
}

type updateFormulaInput struct {
	FormulaID   string  `json:"formula_id" jsonschema:"id of the formula to change"`
	Name        *string `json:"name,omitempty" jsonschema:"omit to keep"`
	Expression  *string `json:"expression,omitempty" jsonschema:"omit to keep"`
	ForLabExam  *bool   `json:"for_lab_exam,omitempty" jsonschema:"omit to keep; can't change while modules use the formula"`
	MakeDefault *bool   `json:"make_default,omitempty" jsonschema:"true to make it the default for its kind"`
}

type deleteFormulaInput struct {
	FormulaID string `json:"formula_id" jsonschema:"id of the formula to delete; it must not be used by any module"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"must be true to delete; only set it after the user confirmed the deletion"`
}

type validateFormulaInput struct {
	Expression string `json:"expression" jsonschema:"formula expression to check"`
	ForLabExam bool   `json:"for_lab_exam,omitempty" jsonschema:"check it as a lab formula (allows the Lab variable)"`
}

// ---------- outputs ----------

type semestersOut struct {
	Semesters []semesterOut `json:"semesters"`
}
type semesterResult struct {
	Semester semesterOut `json:"semester"`
}
type deletedOut struct {
	Deleted bool   `json:"deleted"`
	ID      string `json:"id"`
	Note    string `json:"note,omitempty"`
}
type modulesOut struct {
	Modules []moduleOut `json:"modules"`
}
type moduleResult struct {
	Module moduleOut `json:"module"`
}
type moduleGradesOut struct {
	ModuleID   string     `json:"module_id"`
	ModuleName string     `json:"module_name"`
	MaxGrade   float64    `json:"max_grade"`
	HasLabExam bool       `json:"has_lab_exam"`
	Grades     []gradeOut `json:"grades"`
}
type gradeChangeOut struct {
	ModuleID      string          `json:"module_id"`
	ModuleName    string          `json:"module_name"`
	Assessment    string          `json:"assessment"`
	Value         *float64        `json:"value"`
	PreviousValue *float64        `json:"previous_value"`
	Result        moduleResultOut `json:"result" jsonschema:"the module's recalculated result after the change"`
}
type singleGradeOut struct {
	ModuleID   string   `json:"module_id"`
	ModuleName string   `json:"module_name"`
	Assessment string   `json:"assessment"`
	Value      *float64 `json:"value"`
	Entered    bool     `json:"entered"`
	MaxGrade   float64  `json:"max_grade"`
}
type semesterGradesOut struct {
	SemesterID   string            `json:"semester_id"`
	SemesterName string            `json:"semester_name"`
	Modules      []moduleGradesOut `json:"modules"`
}
type gradesByModuleOut struct {
	Semesters []semesterGradesOut `json:"semesters"`
}
type formulasOut struct {
	Formulas []formulaOut `json:"formulas"`
}
type formulaResult struct {
	Formula formulaOut `json:"formula"`
}
type validationOut struct {
	Valid     bool                  `json:"valid"`
	Errors    []engine.FormulaError `json:"errors"`
	Variables []string              `json:"variables"`
}
type semesterAverageOut struct {
	SemesterID   string            `json:"semester_id"`
	SemesterName string            `json:"semester_name"`
	Summary      summaryOut        `json:"summary"`
	Modules      []moduleResultOut `json:"modules"`
}
type overallOut struct {
	Overall   summaryOut               `json:"overall" jsonschema:"across every module of every semester"`
	Semesters []semesterSummaryLineOut `json:"semesters"`
}
type semesterSummaryLineOut struct {
	SemesterID   string     `json:"semester_id"`
	SemesterName string     `json:"semester_name"`
	Summary      summaryOut `json:"summary"`
}
type missingItemOut struct {
	SemesterID   string   `json:"semester_id"`
	SemesterName string   `json:"semester_name"`
	ModuleID     string   `json:"module_id"`
	ModuleName   string   `json:"module_name"`
	Status       string   `json:"status"`
	Missing      []string `json:"missing" jsonschema:"assessments the module's formula needs that are not entered yet"`
	Note         string   `json:"note,omitempty"`
}
type missingOut struct {
	Items        []missingItemOut `json:"items"`
	TotalMissing int              `json:"total_missing"`
}
type progressOut struct {
	SemesterID            string   `json:"semester_id"`
	SemesterName          string   `json:"semester_name"`
	ModulesTotal          int      `json:"modules_total"`
	ModulesComplete       int      `json:"modules_complete"`
	AssessmentsExpected   int      `json:"assessments_expected" jsonschema:"assessments required by the modules' formulas"`
	AssessmentsEntered    int      `json:"assessments_entered"`
	PercentComplete       *float64 `json:"percent_complete" jsonschema:"entered / expected * 100, or null when nothing is expected"`
	CompletedCoefficient  float64  `json:"completed_coefficient"`
	TotalCoefficient      float64  `json:"total_coefficient"`
	ModulesWithoutFormula int      `json:"modules_without_formula" jsonschema:"modules whose formula is missing or invalid, so their assessments can't be counted"`
}
type assessmentStatOut struct {
	Assessment string   `json:"assessment"`
	Entered    int      `json:"entered"`
	Missing    int      `json:"missing" jsonschema:"modules whose formula needs this assessment but it isn't entered"`
	Mean       *float64 `json:"mean_percent" jsonschema:"average of entered grades as a percentage of each module's max_grade, null when none"`
	Min        *float64 `json:"min_percent"`
	Max        *float64 `json:"max_percent"`
}
type gradeStatsOut struct {
	Scope       string              `json:"scope" jsonschema:"'all semesters' or the semester name"`
	Assessments []assessmentStatOut `json:"assessments"`
	Note        string              `json:"note"`
}

// ---------- registration ----------

func (a *App) registerTools(s *mcp.Server, granted []string) {
	a.semesterTools(s, granted)
	a.moduleTools(s, granted)
	a.gradeTools(s, granted)
	a.formulaTools(s, granted)
	a.statisticsTools(s, granted)
}

func (a *App) semesterTools(s *mcp.Server, g []string) {
	addTool(a, s, g, toolSpec{Name: "list_semesters", Title: "List semesters", Scope: ScopeSemestersRead, Kind: toolRead,
		Description: "List all of the user's semesters in display order, with their academic year, dates and number of modules. Use it to find semester ids."},
		func(_ context.Context, p *principal, _ noInput) (semestersOut, error) {
			list, err := a.views(p.User.ID, "")
			if err != nil {
				return semestersOut{}, err
			}
			out := semestersOut{Semesters: make([]semesterOut, 0, len(list))}
			for _, v := range list {
				out.Semesters = append(out.Semesters, toSemesterOut(v.Semester, len(v.Modules)))
			}
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_semester", Title: "Get semester", Scope: ScopeSemestersRead, Kind: toolRead,
		Description: "Get one semester's details (name, academic year, dates, number of modules)."},
		func(_ context.Context, p *principal, in semesterIDInput) (semesterResult, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return semesterResult{}, err
			}
			return semesterResult{toSemesterOut(list[0].Semester, len(list[0].Modules))}, nil
		})

	addTool(a, s, g, toolSpec{Name: "create_semester", Title: "Create semester", Scope: ScopeSemestersWrite, Kind: toolCreate,
		Description: "Create a new, empty semester at the end of the list. Only name is required. Pass an idempotency_key when you might retry."},
		func(_ context.Context, p *principal, in createSemesterInput) (semesterResult, error) {
			return idempotent(a, p, "create_semester", in.IdempotencyKey, func() (semesterResult, error) {
				sem, err := a.createSemesterFor(p.User.ID, semesterForm{in.Name, in.AcademicYear, in.StartDate, in.EndDate})
				if err != nil {
					return semesterResult{}, err
				}
				return semesterResult{toSemesterOut(toSemesterDTO(*sem), 0)}, nil
			})
		})

	addTool(a, s, g, toolSpec{Name: "update_semester", Title: "Update semester", Scope: ScopeSemestersWrite, Kind: toolUpdate,
		Description: "Change a semester's name, academic year or dates. Only the fields you pass change; pass an empty string to clear an optional field."},
		func(_ context.Context, p *principal, in updateSemesterInput) (semesterResult, error) {
			cur, err := a.ownedSemester(a.DB, p.User.ID, in.SemesterID)
			if err != nil {
				return semesterResult{}, err
			}
			form := semesterForm{cur.Name, optionalText(cur.AcademicYear, in.AcademicYear), optionalText(cur.StartDate, in.StartDate), optionalText(cur.EndDate, in.EndDate)}
			if in.Name != nil {
				form.Name = *in.Name
			}
			sem, err := a.updateSemesterFor(p.User.ID, in.SemesterID, form)
			if err != nil {
				return semesterResult{}, err
			}
			var count int64
			a.DB.Model(&models.Module{}).Where("semester_id = ? AND user_id = ?", sem.ID, p.User.ID).Count(&count)
			return semesterResult{toSemesterOut(toSemesterDTO(*sem), int(count))}, nil
		})

	addTool(a, s, g, toolSpec{Name: "delete_semester", Title: "Delete semester", Scope: ScopeSemestersWrite, Kind: toolDelete,
		Description: "Permanently delete a semester AND all of its modules and grades. Confirm with the user first, then pass confirm=true."},
		func(_ context.Context, p *principal, in deleteSemesterInput) (deletedOut, error) {
			if err := requireConfirm(in.Confirm, "a semester and all of its modules"); err != nil {
				return deletedOut{}, err
			}
			var count int64
			a.DB.Model(&models.Module{}).Where("semester_id = ? AND user_id = ?", in.SemesterID, p.User.ID).Count(&count)
			if err := a.deleteSemesterFor(p.User.ID, in.SemesterID); err != nil {
				return deletedOut{}, err
			}
			return deletedOut{true, in.SemesterID, fmt.Sprintf("%d module(s) were deleted with it.", count)}, nil
		})
}

func (a *App) moduleTools(s *mcp.Server, g []string) {
	addTool(a, s, g, toolSpec{Name: "list_modules", Title: "List modules", Scope: ScopeModulesRead, Kind: toolRead,
		Description: "List modules with their settings (code, coefficient, grading scale, pass mark, lab exam, formula). Pass semester_id for one semester, or omit it for all. Grades are not included; use list_grades or get_grades_by_module."},
		func(_ context.Context, p *principal, in optionalSemesterInput) (modulesOut, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return modulesOut{}, err
			}
			out := modulesOut{Modules: []moduleOut{}}
			for _, sv := range list {
				for _, mv := range sv.Modules {
					out.Modules = append(out.Modules, toModuleOut(mv))
				}
			}
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_module", Title: "Get module", Scope: ScopeModulesRead, Kind: toolRead,
		Description: "Get one module's settings: semester, code, coefficient, max grade, passing threshold, lab exam and formula."},
		func(_ context.Context, p *principal, in moduleIDInput) (moduleResult, error) {
			mv, _, err := a.moduleView(p.User.ID, in.ModuleID)
			if err != nil {
				return moduleResult{}, err
			}
			return moduleResult{toModuleOut(*mv)}, nil
		})

	addTool(a, s, g, toolSpec{Name: "create_module", Title: "Create module", Scope: ScopeModulesWrite, Kind: toolCreate,
		Description: "Add a module to a semester. Defaults: coefficient 1, max_grade 20, passing_threshold 10, no lab exam, and the user's default formula for that kind. Grades start empty; add them with add_grade. Pass an idempotency_key when you might retry."},
		func(_ context.Context, p *principal, in createModuleInput) (moduleResult, error) {
			return idempotent(a, p, "create_module", in.IdempotencyKey, func() (moduleResult, error) {
				form := moduleForm{Name: in.Name, Code: in.Code, Coefficient: 1, MaxGrade: 20, PassingThreshold: 10, HasLabExam: in.HasLabExam, FormulaID: in.FormulaID}
				if in.Coefficient != nil {
					form.Coefficient = *in.Coefficient
				}
				if in.MaxGrade != nil {
					form.MaxGrade = *in.MaxGrade
				}
				if in.PassingThreshold != nil {
					form.PassingThreshold = *in.PassingThreshold
				}
				m, err := a.addModuleFor(p.User.ID, in.SemesterID, form)
				if err != nil {
					return moduleResult{}, err
				}
				mv, _, err := a.moduleView(p.User.ID, m.ID)
				if err != nil {
					return moduleResult{}, err
				}
				return moduleResult{toModuleOut(*mv)}, nil
			})
		})

	addTool(a, s, g, toolSpec{Name: "update_module", Title: "Update module", Scope: ScopeModulesWrite, Kind: toolUpdate,
		Description: "Change a module's settings. Only the fields you pass change. Grades are kept and must still fit a new max_grade. Moving a module to another semester is not supported by Gradly."},
		func(_ context.Context, p *principal, in updateModuleInput) (moduleResult, error) {
			cur, err := a.ownedModule(a.DB, p.User.ID, in.ModuleID)
			if err != nil {
				return moduleResult{}, err
			}
			form := moduleForm{Name: cur.Name, Code: optionalText(cur.Code, in.Code), Coefficient: cur.Coefficient, MaxGrade: cur.MaxGrade,
				PassingThreshold: cur.PassingThreshold, HasLabExam: cur.HasLabExam, FormulaID: cur.FormulaID}
			if in.Name != nil {
				form.Name = *in.Name
			}
			if in.Coefficient != nil {
				form.Coefficient = *in.Coefficient
			}
			if in.MaxGrade != nil {
				form.MaxGrade = *in.MaxGrade
			}
			if in.PassingThreshold != nil {
				form.PassingThreshold = *in.PassingThreshold
			}
			if in.HasLabExam != nil && *in.HasLabExam != cur.HasLabExam {
				form.HasLabExam, form.FormulaID = *in.HasLabExam, nil // the old formula is for the other kind
			}
			if in.FormulaID != nil {
				form.FormulaID = in.FormulaID
			}
			if err := a.updateModuleFor(p.User.ID, in.ModuleID, form); err != nil {
				return moduleResult{}, err
			}
			mv, _, err := a.moduleView(p.User.ID, in.ModuleID)
			if err != nil {
				return moduleResult{}, err
			}
			return moduleResult{toModuleOut(*mv)}, nil
		})

	addTool(a, s, g, toolSpec{Name: "set_module_formula", Title: "Set module formula", Scope: ScopeModulesWrite, Kind: toolUpdate,
		Description: "Choose which grading formula a module uses. The formula must match the module's kind (with or without a lab exam). Omit formula_id to use the user's default formula."},
		func(_ context.Context, p *principal, in setModuleFormulaInput) (moduleResult, error) {
			var requested *string
			if in.FormulaID != "" {
				requested = &in.FormulaID
			}
			if err := a.setModuleFormulaFor(p.User.ID, in.ModuleID, requested); err != nil {
				return moduleResult{}, err
			}
			mv, _, err := a.moduleView(p.User.ID, in.ModuleID)
			if err != nil {
				return moduleResult{}, err
			}
			return moduleResult{toModuleOut(*mv)}, nil
		})

	addTool(a, s, g, toolSpec{Name: "delete_module", Title: "Delete module", Scope: ScopeModulesWrite, Kind: toolDelete,
		Description: "Permanently delete a module and its grades. Confirm with the user first, then pass confirm=true."},
		func(_ context.Context, p *principal, in deleteModuleInput) (deletedOut, error) {
			if err := requireConfirm(in.Confirm, "a module and its grades"); err != nil {
				return deletedOut{}, err
			}
			if err := a.deleteModuleFor(p.User.ID, in.ModuleID); err != nil {
				return deletedOut{}, err
			}
			return deletedOut{Deleted: true, ID: in.ModuleID}, nil
		})
}

func (a *App) gradeTools(s *mcp.Server, g []string) {
	enum := map[string][]any{"assessment": assessmentEnum}

	addTool(a, s, g, toolSpec{Name: "list_grades", Title: "List module grades", Scope: ScopeGradesRead, Kind: toolRead,
		Description: "List a module's assessment grades (CC1, CC2, Lab when the module has a lab exam, Exam). A null value means not entered yet - never a zero."},
		func(_ context.Context, p *principal, in moduleIDInput) (moduleGradesOut, error) {
			m, err := a.ownedModule(a.DB, p.User.ID, in.ModuleID)
			if err != nil {
				return moduleGradesOut{}, err
			}
			return moduleGradesOut{m.ID, m.Name, m.MaxGrade, m.HasLabExam, gradeList(m.CC1, m.CC2, m.LabGrade, m.ExamGrade, m.HasLabExam)}, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_grade", Title: "Get grade", Scope: ScopeGradesRead, Kind: toolRead, Enums: enum,
		Description: "Get one assessment grade of a module. entered=false means the grade hasn't been entered yet."},
		func(_ context.Context, p *principal, in gradeInput) (singleGradeOut, error) {
			as, err := canonicalAssessment(in.Assessment)
			if err != nil {
				return singleGradeOut{}, err
			}
			m, err := a.ownedModule(a.DB, p.User.ID, in.ModuleID)
			if err != nil {
				return singleGradeOut{}, err
			}
			if as == engine.VarLab && !m.HasLabExam {
				return singleGradeOut{}, &toolError{"validation", "This module has no lab exam."}
			}
			v := gradeOf(m, as)
			return singleGradeOut{m.ID, m.Name, as, v, v != nil, m.MaxGrade}, nil
		})

	setGrade := func(p *principal, in gradeValueInput, mustBeEmpty bool) (gradeChangeOut, error) {
		as, err := canonicalAssessment(in.Assessment)
		if err != nil {
			return gradeChangeOut{}, err
		}
		if math.IsNaN(in.Value) || math.IsInf(in.Value, 0) {
			return gradeChangeOut{}, &toolError{"validation", "value must be a number."}
		}
		value := in.Value
		var previous *float64
		_, err = a.changeGrades(p.User.ID, in.ModuleID, func(m *models.Module) (gradesForm, error) {
			if as == engine.VarLab && !m.HasLabExam {
				return gradesForm{}, &toolError{"validation", "This module has no lab exam. Enable it with update_module (has_lab_exam=true) first."}
			}
			previous = gradeOf(m, as)
			if mustBeEmpty && previous != nil {
				return gradesForm{}, &toolError{"duplicate", fmt.Sprintf("%s already has a grade (%s). Use update_grade to change it.", as, engine.FormatNumber(*previous))}
			}
			return withGrade(gradesOf(m), as, &value), nil
		})
		if err != nil {
			return gradeChangeOut{}, err
		}
		return a.gradeChange(p, in.ModuleID, as, previous)
	}

	addTool(a, s, g, toolSpec{Name: "add_grade", Title: "Add grade", Scope: ScopeGradesWrite, Kind: toolCreate, Enums: enum,
		Description: "Enter a grade for an assessment that has no grade yet. Fails if the assessment already has a grade, so retrying never overwrites; use update_grade to change an existing grade. Returns the module's recalculated result."},
		func(_ context.Context, p *principal, in gradeValueInput) (gradeChangeOut, error) {
			return setGrade(p, in, true)
		})

	addTool(a, s, g, toolSpec{Name: "update_grade", Title: "Update grade", Scope: ScopeGradesWrite, Kind: toolUpdate, Enums: enum,
		Description: "Set an assessment's grade, replacing any existing value (safe to retry). Returns the previous value and the module's recalculated result."},
		func(_ context.Context, p *principal, in gradeValueInput) (gradeChangeOut, error) {
			return setGrade(p, in, false)
		})

	addTool(a, s, g, toolSpec{Name: "delete_grade", Title: "Remove grade", Scope: ScopeGradesWrite, Kind: toolDelete, Enums: enum,
		Description: "Remove an assessment's grade so it counts as not entered (not zero). Safe to retry. Returns the previous value and the recalculated result."},
		func(_ context.Context, p *principal, in gradeInput) (gradeChangeOut, error) {
			as, err := canonicalAssessment(in.Assessment)
			if err != nil {
				return gradeChangeOut{}, err
			}
			var previous *float64
			_, err = a.changeGrades(p.User.ID, in.ModuleID, func(m *models.Module) (gradesForm, error) {
				if as == engine.VarLab && !m.HasLabExam {
					return gradesForm{}, &toolError{"validation", "This module has no lab exam."}
				}
				previous = gradeOf(m, as)
				return withGrade(gradesOf(m), as, nil), nil
			})
			if err != nil {
				return gradeChangeOut{}, err
			}
			return a.gradeChange(p, in.ModuleID, as, previous)
		})

	addTool(a, s, g, toolSpec{Name: "get_grades_by_module", Title: "Grades by module", Scope: ScopeGradesRead, Kind: toolRead,
		Description: "Get every grade grouped by semester and module. Pass semester_id for one semester (all grades of that semester), or omit it for all semesters."},
		func(_ context.Context, p *principal, in optionalSemesterInput) (gradesByModuleOut, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return gradesByModuleOut{}, err
			}
			out := gradesByModuleOut{Semesters: make([]semesterGradesOut, 0, len(list))}
			for _, sv := range list {
				sg := semesterGradesOut{sv.Semester.ID, sv.Semester.Name, make([]moduleGradesOut, 0, len(sv.Modules))}
				for _, mv := range sv.Modules {
					m := mv.Module
					sg.Modules = append(sg.Modules, moduleGradesOut{m.ID, m.Name, m.MaxGrade, m.HasLabExam, gradeList(m.CC1, m.CC2, m.Lab, m.Exam, m.HasLabExam)})
				}
				out.Semesters = append(out.Semesters, sg)
			}
			return out, nil
		})
}

func (a *App) gradeChange(p *principal, moduleID, assessment string, previous *float64) (gradeChangeOut, error) {
	mv, _, err := a.moduleView(p.User.ID, moduleID)
	if err != nil {
		return gradeChangeOut{}, err
	}
	m := mv.Module
	value := map[string]*float64{engine.VarCC1: m.CC1, engine.VarCC2: m.CC2, engine.VarLab: m.Lab, engine.VarExam: m.Exam}[assessment]
	return gradeChangeOut{m.ID, m.Name, assessment, value, previous, toResultOut(*mv)}, nil
}

func (a *App) formulaTools(s *mcp.Server, g []string) {
	addTool(a, s, g, toolSpec{Name: "list_formulas", Title: "List formulas", Scope: ScopeFormulasRead, Kind: toolRead,
		Description: "List the user's grading formulas with their expression, kind (lab / no lab), whether each is the default and how many modules use it."},
		func(_ context.Context, p *principal, in listFormulasInput) (formulasOut, error) {
			list, err := a.userFormulas(p.User.ID)
			if err != nil {
				return formulasOut{}, err
			}
			usage := a.formulaUsage(p.User.ID)
			out := formulasOut{Formulas: []formulaOut{}}
			for _, f := range list {
				if in.ForLabExam == nil || *in.ForLabExam == f.ForLabExam {
					out.Formulas = append(out.Formulas, toFormulaOut(f, usage[f.ID]))
				}
			}
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_formula", Title: "Get formula", Scope: ScopeFormulasRead, Kind: toolRead,
		Description: "Get one formula: its expression, the assessments it uses, its kind and how many modules use it."},
		func(_ context.Context, p *principal, in formulaIDInput) (formulaResult, error) {
			var f models.Formula
			if err := a.DB.Where("id = ? AND user_id = ?", in.FormulaID, p.User.ID).First(&f).Error; err != nil {
				return formulaResult{}, notFound("That formula")
			}
			return formulaResult{toFormulaOut(f, a.formulaUsage(p.User.ID)[f.ID])}, nil
		})

	addTool(a, s, g, toolSpec{Name: "validate_formula", Title: "Check a formula", Scope: ScopeFormulasRead, Kind: toolRead,
		Description: "Check a formula expression with Gradly's formula parser without saving anything. Returns errors with their position and the assessments used."},
		func(_ context.Context, _ *principal, in validateFormulaInput) (validationOut, error) {
			parsed := engine.Parse(in.Expression, in.ForLabExam)
			errs := parsed.Errors
			if errs == nil {
				errs = []engine.FormulaError{}
			}
			if parsed.Success() {
				if msg := validateExpression(in.Expression, in.ForLabExam); msg != "" {
					errs = append(errs, engine.FormulaError{Message: msg, Position: -1})
				}
			}
			return validationOut{len(errs) == 0, errs, formulaVariables(in.Expression, in.ForLabExam)}, nil
		})

	addTool(a, s, g, toolSpec{Name: "create_formula", Title: "Create formula", Scope: ScopeFormulasWrite, Kind: toolCreate,
		Description: "Create a grading formula. It is validated by Gradly's formula parser; use validate_formula first if unsure. Names must be unique. Pass an idempotency_key when you might retry."},
		func(_ context.Context, p *principal, in createFormulaInput) (formulaResult, error) {
			return idempotent(a, p, "create_formula", in.IdempotencyKey, func() (formulaResult, error) {
				f, err := a.saveFormula(p.User.ID, "", formulaForm{in.Name, in.Expression, in.ForLabExam, in.MakeDefault})
				if err != nil {
					return formulaResult{}, err
				}
				return formulaResult{toFormulaOut(*f, 0)}, nil
			})
		})

	addTool(a, s, g, toolSpec{Name: "update_formula", Title: "Update formula", Scope: ScopeFormulasWrite, Kind: toolUpdate,
		Description: "Change a formula's name, expression, kind or default status. Only the fields you pass change. Every module using it is recalculated immediately with the new expression."},
		func(_ context.Context, p *principal, in updateFormulaInput) (formulaResult, error) {
			var cur models.Formula
			if err := a.DB.Where("id = ? AND user_id = ?", in.FormulaID, p.User.ID).First(&cur).Error; err != nil {
				return formulaResult{}, notFound("That formula")
			}
			form := formulaForm{cur.Name, cur.Expression, cur.ForLabExam, cur.IsDefault}
			if in.Name != nil {
				form.Name = *in.Name
			}
			if in.Expression != nil {
				form.Expression = *in.Expression
			}
			if in.ForLabExam != nil {
				form.ForLabExam = *in.ForLabExam
			}
			if in.MakeDefault != nil {
				form.IsDefault = *in.MakeDefault
			}
			f, err := a.saveFormula(p.User.ID, in.FormulaID, form)
			if err != nil {
				return formulaResult{}, err
			}
			return formulaResult{toFormulaOut(*f, a.formulaUsage(p.User.ID)[f.ID])}, nil
		})

	addTool(a, s, g, toolSpec{Name: "set_default_formula", Title: "Set default formula", Scope: ScopeFormulasWrite, Kind: toolUpdate,
		Description: "Make a formula the default for its kind (lab or no lab). New modules without an explicit formula use it."},
		func(_ context.Context, p *principal, in formulaIDInput) (formulaResult, error) {
			if err := a.setDefaultFormulaFor(p.User.ID, in.FormulaID); err != nil {
				return formulaResult{}, err
			}
			var f models.Formula
			a.DB.Where("id = ? AND user_id = ?", in.FormulaID, p.User.ID).First(&f)
			return formulaResult{toFormulaOut(f, a.formulaUsage(p.User.ID)[f.ID])}, nil
		})

	addTool(a, s, g, toolSpec{Name: "delete_formula", Title: "Delete formula", Scope: ScopeFormulasWrite, Kind: toolDelete,
		Description: "Permanently delete a formula. It must not be used by any module (reassign them with set_module_formula first). Confirm with the user, then pass confirm=true."},
		func(_ context.Context, p *principal, in deleteFormulaInput) (deletedOut, error) {
			if err := requireConfirm(in.Confirm, "a formula"); err != nil {
				return deletedOut{}, err
			}
			if err := a.deleteFormulaFor(p.User.ID, in.FormulaID); err != nil {
				return deletedOut{}, err
			}
			return deletedOut{Deleted: true, ID: in.FormulaID}, nil
		})
}

func (a *App) statisticsTools(s *mcp.Server, g []string) {
	addTool(a, s, g, toolSpec{Name: "get_module_average", Title: "Module result", Scope: ScopeStatisticsRead, Kind: toolRead,
		Description: "Calculate a module's current result with its formula: the grade and pass/fail when complete, otherwise which assessments are missing and the exam grade needed to pass (when the formula allows a reliable answer)."},
		func(_ context.Context, p *principal, in moduleIDInput) (moduleResultOut, error) {
			mv, _, err := a.moduleView(p.User.ID, in.ModuleID)
			if err != nil {
				return moduleResultOut{}, err
			}
			return toResultOut(*mv), nil
		})

	addTool(a, s, g, toolSpec{Name: "get_semester_average", Title: "Semester average", Scope: ScopeStatisticsRead, Kind: toolRead,
		Description: "Calculate a semester's coefficient-weighted average and pass/fail counts, plus each module's result. Incomplete modules are excluded from the average (is_provisional tells you so), never counted as zero."},
		func(_ context.Context, p *principal, in semesterIDInput) (semesterAverageOut, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return semesterAverageOut{}, err
			}
			sv := list[0]
			out := semesterAverageOut{sv.Semester.ID, sv.Semester.Name, toSummaryOut(sv.Summary), make([]moduleResultOut, 0, len(sv.Modules))}
			for _, mv := range sv.Modules {
				out.Modules = append(out.Modules, toResultOut(mv))
			}
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_overall_statistics", Title: "Overall statistics", Scope: ScopeStatisticsRead, Kind: toolRead,
		Description: "Overall academic statistics: the weighted average across all modules of all semesters, and each semester's average and counts."},
		func(_ context.Context, p *principal, _ noInput) (overallOut, error) {
			list, err := a.views(p.User.ID, "")
			if err != nil {
				return overallOut{}, err
			}
			scores := []engine.ModuleScore{}
			out := overallOut{Semesters: make([]semesterSummaryLineOut, 0, len(list))}
			for _, sv := range list {
				for _, mv := range sv.Modules {
					scores = append(scores, mv.score())
				}
				out.Semesters = append(out.Semesters, semesterSummaryLineOut{sv.Semester.ID, sv.Semester.Name, toSummaryOut(sv.Summary)})
			}
			out.Overall = toSummaryOut(engine.Summarize(scores))
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_missing_assessments", Title: "Missing assessments", Scope: ScopeStatisticsRead, Kind: toolRead,
		Description: "List the modules that still lack grades their formula needs, and which assessments are missing. Pass semester_id for one semester or omit it for all. Modules without a valid formula are listed with a note."},
		func(_ context.Context, p *principal, in optionalSemesterInput) (missingOut, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return missingOut{}, err
			}
			out := missingOut{Items: []missingItemOut{}}
			for _, sv := range list {
				for _, mv := range sv.Modules {
					r := mv.Result
					item := missingItemOut{sv.Semester.ID, sv.Semester.Name, mv.Module.ID, mv.Module.Name, string(r.Status), r.MissingInputs, ""}
					if item.Missing == nil {
						item.Missing = []string{}
					}
					switch r.Status {
					case engine.StatusNoFormula:
						item.Note = "No formula is selected, so Gradly can't tell which grades are needed."
					case engine.StatusFormulaError:
						item.Note = "The module's formula is invalid, so Gradly can't tell which grades are needed."
					}
					if len(item.Missing) > 0 || item.Note != "" {
						out.TotalMissing += len(item.Missing)
						out.Items = append(out.Items, item)
					}
				}
			}
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_semester_progress", Title: "Semester progress", Scope: ScopeStatisticsRead, Kind: toolRead,
		Description: "How far along a semester is: modules complete, assessments entered versus required by the formulas, and the share of the total coefficient already graded."},
		func(_ context.Context, p *principal, in semesterIDInput) (progressOut, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return progressOut{}, err
			}
			sv := list[0]
			out := progressOut{SemesterID: sv.Semester.ID, SemesterName: sv.Semester.Name, ModulesTotal: len(sv.Modules),
				ModulesComplete: sv.Summary.CompletedCount, CompletedCoefficient: sv.Summary.CompletedCoefficient, TotalCoefficient: sv.Summary.TotalCoefficient}
			for _, mv := range sv.Modules {
				if mv.Formula == nil || mv.Result.Status == engine.StatusFormulaError {
					out.ModulesWithoutFormula++
					continue
				}
				vars := formulaVariables(mv.Formula.Expression, mv.Module.HasLabExam)
				out.AssessmentsExpected += len(vars)
				out.AssessmentsEntered += len(vars) - len(mv.Result.MissingInputs)
			}
			if out.AssessmentsExpected > 0 {
				pct := engine.Round(float64(out.AssessmentsEntered) / float64(out.AssessmentsExpected) * 100)
				out.PercentComplete = &pct
			}
			return out, nil
		})

	addTool(a, s, g, toolSpec{Name: "get_grade_statistics", Title: "Grade statistics", Scope: ScopeStatisticsRead, Kind: toolRead,
		Description: "Per-assessment statistics (CC1, CC2, Lab, Exam): how many grades are entered or missing and their mean/min/max as a percentage of each module's max grade. Pass semester_id or omit it for all semesters."},
		func(_ context.Context, p *principal, in optionalSemesterInput) (gradeStatsOut, error) {
			list, err := a.views(p.User.ID, in.SemesterID)
			if err != nil {
				return gradeStatsOut{}, err
			}
			scope := "all semesters"
			if in.SemesterID != "" {
				scope = list[0].Semester.Name
			}
			type acc struct {
				entered, missing int
				sum, min, max    float64
			}
			stats := map[string]*acc{}
			for _, v := range engine.AllVariables {
				stats[v] = &acc{min: math.Inf(1), max: math.Inf(-1)}
			}
			for _, sv := range list {
				for _, mv := range sv.Modules {
					m := mv.Module
					for _, gr := range gradeList(m.CC1, m.CC2, m.Lab, m.Exam, m.HasLabExam) {
						st := stats[gr.Assessment]
						if gr.Value == nil {
							continue
						}
						pct := *gr.Value / m.MaxGrade * 100
						st.entered++
						st.sum += pct
						st.min, st.max = math.Min(st.min, pct), math.Max(st.max, pct)
					}
					for _, miss := range mv.Result.MissingInputs {
						stats[miss].missing++
					}
				}
			}
			out := gradeStatsOut{Scope: scope, Assessments: []assessmentStatOut{},
				Note: "Percentages make grades on different scales comparable. Only entered grades are counted; missing grades are not zeros."}
			for _, v := range engine.AllVariables {
				st := stats[v]
				line := assessmentStatOut{Assessment: v, Entered: st.entered, Missing: st.missing}
				if st.entered > 0 {
					mean, lo, hi := engine.Round(st.sum/float64(st.entered)), engine.Round(st.min), engine.Round(st.max)
					line.Mean, line.Min, line.Max = &mean, &lo, &hi
				}
				out.Assessments = append(out.Assessments, line)
			}
			return out, nil
		})
}
