package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/engine"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

const (
	defaultNoLabName = "Standard (no lab)"
	defaultLabName   = "Standard (with lab)"
)

type formulaForm struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
	ForLabExam bool   `json:"forLabExam"`
	IsDefault  bool   `json:"isDefault"`
}

// validateExpression returns a user-facing message, or "" when the expression is acceptable.
func validateExpression(expression string, forLab bool) string {
	parsed := engine.Parse(expression, forLab)
	if !parsed.Success() {
		return parsed.FirstError()
	}
	if len(parsed.Formula.Variables) == 0 {
		return "A formula must use at least one variable (CC1, CC2, Lab or Exam)."
	}
	return ""
}

func platformLabFormula(db *gorm.DB) string {
	var s models.Setting
	if err := db.Where("key = ?", models.SettingDefaultLabFormula).First(&s).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(s.Value)
}

type formulaJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Expression   string `json:"expression"`
	ForLabExam   bool   `json:"forLabExam"`
	IsDefault    bool   `json:"isDefault"`
	ModulesUsing int    `json:"modulesUsing"`
}

func (a *App) listFormulas(c *gin.Context) {
	user := current(c)
	formulas, err := a.userFormulas(user.ID)
	if err != nil {
		fail(c, err)
		return
	}
	type usage struct {
		FormulaID string
		N         int
	}
	var rows []usage
	a.DB.Model(&models.Module{}).Select("formula_id, COUNT(*) AS n").
		Where("user_id = ? AND formula_id IS NOT NULL", user.ID).Group("formula_id").Scan(&rows)
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.FormulaID] = r.N
	}
	out := make([]formulaJSON, 0, len(formulas))
	for _, f := range formulas {
		out = append(out, formulaJSON{f.ID, f.Name, f.Expression, f.ForLabExam, f.IsDefault, counts[f.ID]})
	}
	c.JSON(http.StatusOK, gin.H{
		"formulas":         out,
		"platformLab":      nilIfEmpty(platformLabFormula(a.DB)),
		"variablesNoLab":   engine.NoLabVariables,
		"variablesWithLab": engine.AllVariables,
		"functions":        engine.Functions(),
		"defaultNoLab":     engine.DefaultNoLabFormula,
		"maxLength":        engine.MaxFormulaLength,
	})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// saveFormula creates (id == "") or updates a formula inside one transaction.
func (a *App) saveFormula(userID, id string, in formulaForm) (*models.Formula, error) {
	name := strings.TrimSpace(in.Name)
	if len([]rune(name)) == 0 || len([]rune(name)) > 80 {
		return nil, badRequest("Give the formula a name (up to 80 characters).")
	}
	if msg := validateExpression(in.Expression, in.ForLabExam); msg != "" {
		return nil, badRequest(msg)
	}

	var saved models.Formula
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var clash int64
		tx.Model(&models.Formula{}).Where("user_id = ? AND id <> ? AND LOWER(name) = ?", userID, id, strings.ToLower(name)).Count(&clash)
		if clash > 0 {
			return badRequest("You already have a formula with that name.")
		}

		var f models.Formula
		if id == "" {
			f = models.Formula{UserID: userID}
		} else {
			if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&f).Error; err != nil {
				return notFound("That formula")
			}
			if f.ForLabExam != in.ForLabExam {
				var using int64
				tx.Model(&models.Module{}).Where("formula_id = ? AND user_id = ?", f.ID, userID).Count(&using)
				if using > 0 {
					return badRequest("This formula is used by modules, so its lab/no-lab type can't change. Reassign those modules first.")
				}
			}
		}
		f.Name, f.Expression, f.ForLabExam, f.UpdatedAt = name, strings.TrimSpace(in.Expression), in.ForLabExam, a.now()

		var otherDefault int64
		tx.Model(&models.Formula{}).Where("user_id = ? AND for_lab_exam = ? AND is_default = ? AND id <> ?", userID, in.ForLabExam, true, f.ID).Count(&otherDefault)
		makeDefault := in.IsDefault || otherDefault == 0
		f.IsDefault = makeDefault
		if err := tx.Save(&f).Error; err != nil {
			return err
		}
		if makeDefault {
			if err := clearDefault(tx, userID, in.ForLabExam, f.ID); err != nil {
				return err
			}
		}
		saved = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func clearDefault(tx *gorm.DB, userID string, forLab bool, exceptID string) error {
	return tx.Model(&models.Formula{}).
		Where("user_id = ? AND for_lab_exam = ? AND is_default = ? AND id <> ?", userID, forLab, true, exceptID).
		Update("is_default", false).Error
}

func (a *App) createFormula(c *gin.Context) {
	var in formulaForm
	if !bind(c, &in) {
		return
	}
	f, err := a.saveFormula(current(c).ID, "", in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, toFormulaJSON(*f))
}

func (a *App) updateFormula(c *gin.Context) {
	var in formulaForm
	if !bind(c, &in) {
		return
	}
	f, err := a.saveFormula(current(c).ID, c.Param("id"), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, toFormulaJSON(*f))
}

func (a *App) setDefaultFormula(c *gin.Context) {
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var f models.Formula
		if err := tx.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&f).Error; err != nil {
			return notFound("That formula")
		}
		if err := clearDefault(tx, user.ID, f.ForLabExam, f.ID); err != nil {
			return err
		}
		return tx.Model(&f).Update("is_default", true).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *App) deleteFormula(c *gin.Context) {
	user := current(c)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var f models.Formula
		if err := tx.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).First(&f).Error; err != nil {
			return notFound("That formula")
		}
		var using int64
		tx.Model(&models.Module{}).Where("formula_id = ? AND user_id = ?", f.ID, user.ID).Count(&using)
		if using > 0 {
			plural := "s"
			if using == 1 {
				plural = ""
			}
			return badRequest("This formula is used by " + itoa(using) + " module" + plural + ". Reassign them to another formula before deleting it.")
		}
		return tx.Delete(&f).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// addPlatformLabFormula adds the platform's default lab formula (set by the administrator) to the current user.
func (a *App) addPlatformLabFormula(c *gin.Context) {
	user := current(c)
	var created models.Formula
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		expression := platformLabFormula(tx)
		if expression == "" {
			return badRequest("The administrator hasn't configured a default lab formula yet.")
		}
		var have int64
		tx.Model(&models.Formula{}).Where("user_id = ? AND name = ?", user.ID, defaultLabName).Count(&have)
		if have > 0 {
			return badRequest("You already have the platform's lab formula.")
		}
		var hasDefault int64
		tx.Model(&models.Formula{}).Where("user_id = ? AND for_lab_exam = ? AND is_default = ?", user.ID, true, true).Count(&hasDefault)
		created = models.Formula{UserID: user.ID, Name: defaultLabName, Expression: expression, ForLabExam: true,
			IsDefault: hasDefault == 0, UpdatedAt: a.now()}
		return tx.Create(&created).Error
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, toFormulaJSON(created))
}

// previewFormula parses an expression and shows what it gives for sample grades (out of 20, pass mark 10).
func (a *App) previewFormula(c *gin.Context) {
	var in struct {
		Expression string   `json:"expression"`
		ForLabExam bool     `json:"forLabExam"`
		CC1        *float64 `json:"cc1"`
		CC2        *float64 `json:"cc2"`
		Lab        *float64 `json:"lab"`
		Exam       *float64 `json:"exam"`
	}
	if !bind(c, &in) {
		return
	}
	parsed := engine.Parse(in.Expression, in.ForLabExam)
	if !parsed.Success() {
		c.JSON(http.StatusOK, gin.H{"success": false, "errors": parsed.Errors})
		return
	}
	input := func(withExam bool) engine.ModuleInput {
		m := engine.ModuleInput{CC1: in.CC1, CC2: in.CC2, HasLabExam: in.ForLabExam, MaxGrade: 20, PassingThreshold: 10}
		if in.ForLabExam {
			m.Lab = in.Lab
		}
		if withExam {
			m.Exam = in.Exam
		}
		return m
	}
	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"errors":   []engine.FormulaError{},
		"full":     engine.Calculate(input(true), in.Expression),
		"required": engine.Calculate(input(false), in.Expression),
	})
}

func toFormulaJSON(f models.Formula) formulaJSON {
	return formulaJSON{f.ID, f.Name, f.Expression, f.ForLabExam, f.IsDefault, 0}
}
