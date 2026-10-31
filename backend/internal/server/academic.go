package server

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/engine"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

// ---------- views ----------

type formulaRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

type semesterDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	AcademicYear *string   `json:"academicYear"`
	StartDate    *string   `json:"startDate"`
	EndDate      *string   `json:"endDate"`
	SortOrder    int       `json:"sortOrder"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type moduleDTO struct {
	ID               string    `json:"id"`
	SemesterID       string    `json:"semesterId"`
	Name             string    `json:"name"`
	Code             *string   `json:"code"`
	Coefficient      float64   `json:"coefficient"`
	MaxGrade         float64   `json:"maxGrade"`
	PassingThreshold float64   `json:"passingThreshold"`
	HasLabExam       bool      `json:"hasLabExam"`
	CC1              *float64  `json:"cc1"`
	CC2              *float64  `json:"cc2"`
	Lab              *float64  `json:"lab"`
	Exam             *float64  `json:"exam"`
	FormulaID        *string   `json:"formulaId"`
	SortOrder        int       `json:"sortOrder"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type moduleView struct {
	Module  moduleDTO           `json:"module"`
	Formula *formulaRef         `json:"formula"`
	Result  engine.ModuleResult `json:"result"`
}

type semesterView struct {
	Semester semesterDTO            `json:"semester"`
	Modules  []moduleView           `json:"modules"`
	Summary  engine.SemesterSummary `json:"summary"`
}

func toSemesterDTO(s models.Semester) semesterDTO {
	return semesterDTO{s.ID, s.Name, s.AcademicYear, s.StartDate, s.EndDate, s.SortOrder, s.UpdatedAt}
}

func buildModule(m models.Module, formulas map[string]models.Formula) moduleView {
	var ref *formulaRef
	expression := ""
	if m.FormulaID != nil {
		if f, ok := formulas[*m.FormulaID]; ok {
			ref = &formulaRef{f.ID, f.Name, f.Expression}
			expression = f.Expression
		}
	}
	result := engine.Calculate(engine.ModuleInput{
		CC1: m.CC1, CC2: m.CC2, Lab: m.LabGrade, Exam: m.ExamGrade,
		HasLabExam: m.HasLabExam, MaxGrade: m.MaxGrade, PassingThreshold: m.PassingThreshold,
	}, expression)
	dto := moduleDTO{m.ID, m.SemesterID, m.Name, m.Code, m.Coefficient, m.MaxGrade, m.PassingThreshold, m.HasLabExam,
		m.CC1, m.CC2, m.LabGrade, m.ExamGrade, m.FormulaID, m.SortOrder, m.UpdatedAt}
	return moduleView{dto, ref, result}
}

func (v moduleView) score() engine.ModuleScore {
	return engine.ModuleScore{Coefficient: v.Module.Coefficient, MaxGrade: v.Module.MaxGrade, Result: v.Result}
}

// loadSemesters reads a user's semesters (optionally just one) with computed results.
// Callers are responsible for authorising userID.
func (a *App) loadSemesters(userID string, semesterID string) ([]semesterView, error) {
	q := a.DB.Where("user_id = ?", userID)
	if semesterID != "" {
		q = q.Where("id = ?", semesterID)
	}
	var semesters []models.Semester
	if err := q.Order("sort_order, created_at").Find(&semesters).Error; err != nil {
		return nil, err
	}
	out := make([]semesterView, 0, len(semesters))
	if len(semesters) == 0 {
		return out, nil
	}
	ids := make([]string, len(semesters))
	for i, s := range semesters {
		ids[i] = s.ID
	}
	var modules []models.Module
	if err := a.DB.Where("user_id = ? AND semester_id IN ?", userID, ids).Order("sort_order, name").Find(&modules).Error; err != nil {
		return nil, err
	}
	var formulaList []models.Formula
	if err := a.DB.Where("user_id = ?", userID).Find(&formulaList).Error; err != nil {
		return nil, err
	}
	formulas := make(map[string]models.Formula, len(formulaList))
	for _, f := range formulaList {
		formulas[f.ID] = f
	}
	for _, s := range semesters {
		views := []moduleView{}
		scores := []engine.ModuleScore{}
		for _, m := range modules {
			if m.SemesterID == s.ID {
				v := buildModule(m, formulas)
				views = append(views, v)
				scores = append(scores, v.score())
			}
		}
		out = append(out, semesterView{toSemesterDTO(s), views, engine.Summarize(scores)})
	}
	return out, nil
}

// ---------- reads ----------

func (a *App) dashboard(c *gin.Context) {
	semesters, err := a.loadSemesters(current(c).ID, "")
	if err != nil {
		fail(c, err)
		return
	}
	scores := []engine.ModuleScore{}
	var currentView *semesterView
	var best int64 = math.MinInt64
	for i, s := range semesters {
		stamp := s.Semester.UpdatedAt.UnixNano()
		for _, m := range s.Modules {
			scores = append(scores, m.score())
			if t := m.Module.UpdatedAt.UnixNano(); t > stamp {
				stamp = t
			}
		}
		if stamp > best {
			best = stamp
			currentView = &semesters[i]
		}
	}
	c.JSON(http.StatusOK, gin.H{"semesters": semesters, "overall": engine.Summarize(scores), "current": currentView})
}

func (a *App) listSemesters(c *gin.Context) {
	semesters, err := a.loadSemesters(current(c).ID, "")
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, semesters)
}

func (a *App) getSemester(c *gin.Context) {
	user := current(c)
	semesters, err := a.loadSemesters(user.ID, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if len(semesters) == 0 {
		fail(c, notFound("That semester"))
		return
	}
	formulas, err := a.userFormulas(user.ID)
	if err != nil {
		fail(c, err)
		return
	}
	fj := make([]formulaJSON, 0, len(formulas))
	for _, f := range formulas {
		fj = append(fj, toFormulaJSON(f))
	}
	c.JSON(http.StatusOK, gin.H{"view": semesters[0], "formulas": fj})
}

func (a *App) userFormulas(userID string) ([]models.Formula, error) {
	var list []models.Formula
	err := a.DB.Where("user_id = ?", userID).Order("for_lab_exam, name").Find(&list).Error
	if list == nil {
		list = []models.Formula{}
	}
	return list, err
}

// ---------- forms ----------

type semesterForm struct {
	Name         string  `json:"name"`
	AcademicYear *string `json:"academicYear"`
	StartDate    *string `json:"startDate"`
	EndDate      *string `json:"endDate"`
}

type moduleForm struct {
	Name             string  `json:"name"`
	Code             *string `json:"code"`
	Coefficient      float64 `json:"coefficient"`
	MaxGrade         float64 `json:"maxGrade"`
	PassingThreshold float64 `json:"passingThreshold"`
	HasLabExam       bool    `json:"hasLabExam"`
	FormulaID        *string `json:"formulaId"`
}

type gradesForm struct {
	CC1  *float64 `json:"cc1"`
	CC2  *float64 `json:"cc2"`
	Lab  *float64 `json:"lab"`
	Exam *float64 `json:"exam"`
}

func parseDate(s *string) (*string, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(*s))
	if err != nil {
		return nil, badRequest("Enter dates as YYYY-MM-DD.")
	}
	d := t.Format("2006-01-02")
	return &d, nil
}

func validateSemester(f *semesterForm) (start, end *string, err error) {
	name := strings.TrimSpace(f.Name)
	switch {
	case name == "":
		return nil, nil, badRequest("Give the semester a name.")
	case len([]rune(name)) > 100:
		return nil, nil, badRequest("The semester name is too long (max 100).")
	case f.AcademicYear != nil && len([]rune(*f.AcademicYear)) > 50:
		return nil, nil, badRequest("The academic year is too long (max 50).")
	}
	if start, err = parseDate(f.StartDate); err != nil {
		return nil, nil, err
	}
	if end, err = parseDate(f.EndDate); err != nil {
		return nil, nil, err
	}
	if start != nil && end != nil && *end < *start {
		return nil, nil, badRequest("The end date can't be before the start date.")
	}
	return start, end, nil
}

func finiteNum(v float64) bool { return !math.IsInf(v, 0) && !math.IsNaN(v) }

func validateModule(f *moduleForm) error {
	name := strings.TrimSpace(f.Name)
	switch {
	case name == "":
		return badRequest("Give the module a name.")
	case len([]rune(name)) > 100:
		return badRequest("The module name is too long (max 100).")
	case f.Code != nil && len([]rune(*f.Code)) > 30:
		return badRequest("The module code is too long (max 30).")
	case !finiteNum(f.Coefficient) || f.Coefficient < 0 || f.Coefficient > 1000:
		return badRequest("The coefficient must be between 0 and 1000.")
	case !finiteNum(f.MaxGrade) || f.MaxGrade <= 0 || f.MaxGrade > 1000:
		return badRequest("The maximum grade must be greater than 0 (and at most 1000).")
	case !finiteNum(f.PassingThreshold) || f.PassingThreshold < 0 || f.PassingThreshold > f.MaxGrade:
		return badRequest("The passing threshold must be between 0 and the maximum grade.")
	}
	return nil
}

func validateGrades(max float64, grades map[string]*float64) error {
	for _, label := range []string{"CC1", "CC2", "Lab", "Exam"} {
		v := grades[label]
		if v == nil {
			continue
		}
		if !finiteNum(*v) || *v < 0 || *v > max {
			return badRequest(fmt.Sprintf("%s must be between 0 and %s.", label, engine.FormatNumber(max)))
		}
	}
	return nil
}

// ---------- semesters ----------

func (a *App) ownedSemester(tx *gorm.DB, userID, id string) (*models.Semester, error) {
	var s models.Semester
	if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&s).Error; err != nil {
		return nil, notFound("That semester")
	}
	return &s, nil
}

func (a *App) ownedModule(tx *gorm.DB, userID, id string) (*models.Module, error) {
	var m models.Module
	if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&m).Error; err != nil {
		return nil, notFound("That module")
	}
	return &m, nil
}

func (a *App) createSemester(c *gin.Context) {
	var in semesterForm
	if !bind(c, &in) {
		return
	}
	start, end, err := validateSemester(&in)
	if err != nil {
		fail(c, err)
		return
	}
	user := current(c)
	var max *int
	a.DB.Model(&models.Semester{}).Where("user_id = ?", user.ID).Select("MAX(sort_order)").Scan(&max)
	next := 0
	if max != nil {
		next = *max + 1
	}
	now := a.now()
	s := models.Semester{
		UserID: user.ID, Name: strings.TrimSpace(in.Name), AcademicYear: clean(in.AcademicYear),
		StartDate: start, EndDate: end, SortOrder: next, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.DB.Create(&s).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSemesterDTO(s))
}

func (a *App) updateSemester(c *gin.Context) {
	var in semesterForm
	if !bind(c, &in) {
		return
	}
	start, end, err := validateSemester(&in)
	if err != nil {
		fail(c, err)
		return
	}
	s, err := a.ownedSemester(a.DB, current(c).ID, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	s.Name, s.AcademicYear, s.StartDate, s.EndDate, s.UpdatedAt = strings.TrimSpace(in.Name), clean(in.AcademicYear), start, end, a.now()
	if err := a.DB.Save(s).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, toSemesterDTO(*s))
}

func (a *App) deleteSemester(c *gin.Context) {
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		s, err := a.ownedSemester(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		if err := tx.Where("semester_id = ? AND user_id = ?", s.ID, user.ID).Delete(&models.Module{}).Error; err != nil {
			return err
		}
		return tx.Delete(s).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// reorder swaps the target with its neighbour and rewrites 0..n-1. ids is the current order.
func reorder(ids []string, target string, direction int) (changed bool, order []string, err error) {
	index := -1
	for i, id := range ids {
		if id == target {
			index = i
		}
	}
	if index < 0 {
		return false, nil, notFound("")
	}
	step := 0
	if direction > 0 {
		step = 1
	} else if direction < 0 {
		step = -1
	}
	next := index + step
	if step == 0 || next < 0 || next >= len(ids) {
		return false, ids, nil
	}
	ids[index], ids[next] = ids[next], ids[index]
	return true, ids, nil
}

type moveForm struct {
	Direction int `json:"direction"`
}

func (a *App) moveSemester(c *gin.Context) {
	var in moveForm
	if !bind(c, &in) {
		return
	}
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var list []models.Semester
		if err := tx.Where("user_id = ?", user.ID).Order("sort_order, created_at").Find(&list).Error; err != nil {
			return err
		}
		ids := make([]string, len(list))
		for i, s := range list {
			ids[i] = s.ID
		}
		changed, order, err := reorder(ids, c.Param("id"), in.Direction)
		if err != nil || !changed {
			return err
		}
		for i, id := range order {
			if err := tx.Model(&models.Semester{}).Where("id = ?", id).Update("sort_order", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- modules ----------

func (a *App) touchSemester(tx *gorm.DB, userID, semesterID string) {
	tx.Model(&models.Semester{}).Where("id = ? AND user_id = ?", semesterID, userID).Update("updated_at", a.now())
}

// resolveFormula picks a formula that belongs to the user and matches the module kind; it falls back to the
// user's default for that kind.
func resolveFormula(tx *gorm.DB, userID string, requested *string, hasLab bool) (*string, error) {
	if requested != nil && *requested != "" {
		var f models.Formula
		if err := tx.Where("id = ? AND user_id = ?", *requested, userID).First(&f).Error; err != nil {
			return nil, badRequest("That formula isn't available.")
		}
		if f.ForLabExam != hasLab {
			if hasLab {
				return nil, badRequest("Choose a formula made for modules with a lab exam.")
			}
			return nil, badRequest("Choose a formula made for modules without a lab exam.")
		}
		return &f.ID, nil
	}
	var f models.Formula
	err := tx.Where("user_id = ? AND for_lab_exam = ?", userID, hasLab).Order("is_default DESC, name").First(&f).Error
	if err != nil {
		return nil, nil
	}
	return &f.ID, nil
}

func (a *App) addModule(c *gin.Context) {
	var in moduleForm
	if !bind(c, &in) {
		return
	}
	if err := validateModule(&in); err != nil {
		fail(c, err)
		return
	}
	user := current(c)
	var created models.Module
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		s, err := a.ownedSemester(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		var max *int
		tx.Model(&models.Module{}).Where("semester_id = ? AND user_id = ?", s.ID, user.ID).Select("MAX(sort_order)").Scan(&max)
		next := 0
		if max != nil {
			next = *max + 1
		}
		formulaID, err := resolveFormula(tx, user.ID, in.FormulaID, in.HasLabExam)
		if err != nil {
			return err
		}
		created = models.Module{
			SemesterID: s.ID, UserID: user.ID, Name: strings.TrimSpace(in.Name), Code: clean(in.Code),
			Coefficient: in.Coefficient, MaxGrade: in.MaxGrade, PassingThreshold: in.PassingThreshold,
			HasLabExam: in.HasLabExam, FormulaID: formulaID, SortOrder: next, UpdatedAt: a.now(),
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		a.touchSemester(tx, user.ID, s.ID)
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": created.ID})
}

func (a *App) updateModule(c *gin.Context) {
	var in moduleForm
	if !bind(c, &in) {
		return
	}
	if err := validateModule(&in); err != nil {
		fail(c, err)
		return
	}
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		m, err := a.ownedModule(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		var lab *float64
		if m.HasLabExam || in.HasLabExam {
			lab = m.LabGrade
		}
		if err := validateGrades(in.MaxGrade, map[string]*float64{"CC1": m.CC1, "CC2": m.CC2, "Lab": lab, "Exam": m.ExamGrade}); err != nil {
			return err
		}
		formulaID, err := resolveFormula(tx, user.ID, in.FormulaID, in.HasLabExam)
		if err != nil {
			return err
		}
		m.Name, m.Code, m.Coefficient = strings.TrimSpace(in.Name), clean(in.Code), in.Coefficient
		m.MaxGrade, m.PassingThreshold, m.HasLabExam = in.MaxGrade, in.PassingThreshold, in.HasLabExam
		m.FormulaID, m.UpdatedAt = formulaID, a.now()
		if err := tx.Save(m).Error; err != nil {
			return err
		}
		a.touchSemester(tx, user.ID, m.SemesterID)
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *App) updateGrades(c *gin.Context) {
	var in gradesForm
	if !bind(c, &in) {
		return
	}
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		m, err := a.ownedModule(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		var lab *float64
		if m.HasLabExam {
			lab = in.Lab
		}
		if err := validateGrades(m.MaxGrade, map[string]*float64{"CC1": in.CC1, "CC2": in.CC2, "Lab": lab, "Exam": in.Exam}); err != nil {
			return err
		}
		m.CC1, m.CC2, m.ExamGrade = in.CC1, in.CC2, in.Exam
		if m.HasLabExam {
			m.LabGrade = in.Lab // a stored lab grade is kept if the lab is toggled off
		}
		m.UpdatedAt = a.now()
		if err := tx.Save(m).Error; err != nil {
			return err
		}
		a.touchSemester(tx, user.ID, m.SemesterID)
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *App) setModuleFormula(c *gin.Context) {
	var in struct {
		FormulaID *string `json:"formulaId"`
	}
	if !bind(c, &in) {
		return
	}
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		m, err := a.ownedModule(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		formulaID, err := resolveFormula(tx, user.ID, in.FormulaID, m.HasLabExam)
		if err != nil {
			return err
		}
		m.FormulaID, m.UpdatedAt = formulaID, a.now()
		if err := tx.Save(m).Error; err != nil {
			return err
		}
		a.touchSemester(tx, user.ID, m.SemesterID)
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *App) deleteModule(c *gin.Context) {
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		m, err := a.ownedModule(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		if err := tx.Delete(m).Error; err != nil {
			return err
		}
		a.touchSemester(tx, user.ID, m.SemesterID)
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *App) moveModule(c *gin.Context) {
	var in moveForm
	if !bind(c, &in) {
		return
	}
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		m, err := a.ownedModule(tx, user.ID, c.Param("id"))
		if err != nil {
			return err
		}
		var list []models.Module
		if err := tx.Where("semester_id = ? AND user_id = ?", m.SemesterID, user.ID).Order("sort_order, name").Find(&list).Error; err != nil {
			return err
		}
		ids := make([]string, len(list))
		for i, x := range list {
			ids[i] = x.ID
		}
		changed, order, err := reorder(ids, m.ID, in.Direction)
		if err != nil || !changed {
			return err
		}
		for i, id := range order {
			if err := tx.Model(&models.Module{}).Where("id = ?", id).Update("sort_order", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
