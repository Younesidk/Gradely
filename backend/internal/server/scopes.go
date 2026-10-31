package server

import (
	"slices"
	"strings"
)

// Permission scopes shared by API keys and OAuth tokens. They are enforced server-side on every MCP tool call.
const (
	ScopeGradesRead     = "grades:read"
	ScopeGradesWrite    = "grades:write"
	ScopeSemestersRead  = "semesters:read"
	ScopeSemestersWrite = "semesters:write"
	ScopeModulesRead    = "modules:read"
	ScopeModulesWrite   = "modules:write"
	ScopeFormulasRead   = "formulas:read"
	ScopeFormulasWrite  = "formulas:write"
	ScopeStatisticsRead = "statistics:read"
)

type scopeInfo struct {
	Scope       string `json:"scope"`
	Group       string `json:"group"`  // the kind of data
	Access      string `json:"access"` // "read" or "write"
	Label       string `json:"label"`
	Description string `json:"description"`
}

// scopeCatalog is the single source of truth for scope names and their plain-language explanations; the
// Developer Integrations page and the OAuth consent screen render it.
var scopeCatalog = []scopeInfo{
	{ScopeSemestersRead, "semesters", "read", "See semesters", "Semester names, academic years and dates."},
	{ScopeModulesRead, "modules", "read", "See modules", "Module names, codes, coefficients, grading scales and which formula each one uses."},
	{ScopeGradesRead, "grades", "read", "See grades", "Your CC1, CC2, lab and exam grades and each module's calculated result."},
	{ScopeFormulasRead, "formulas", "read", "See formulas", "Your grading formulas and whether they are valid."},
	{ScopeStatisticsRead, "statistics", "read", "See averages and progress", "Module and semester averages, pass/fail counts, missing assessments and the exam grade you need."},
	{ScopeSemestersWrite, "semesters", "write", "Manage semesters", "Create, rename, edit and delete semesters. Deleting a semester deletes its modules."},
	{ScopeModulesWrite, "modules", "write", "Manage modules", "Create, edit and delete modules and choose their formula."},
	{ScopeGradesWrite, "grades", "write", "Enter and change grades", "Add, change and remove CC1, CC2, lab and exam grades."},
	{ScopeFormulasWrite, "formulas", "write", "Manage formulas", "Create, edit and delete grading formulas and choose your defaults."},
}

func allScopes() []string {
	out := make([]string, len(scopeCatalog))
	for i, s := range scopeCatalog {
		out[i] = s.Scope
	}
	return out
}

func readScopes() []string {
	var out []string
	for _, s := range scopeCatalog {
		if s.Access == "read" {
			out = append(out, s.Scope)
		}
	}
	return out
}

func knownScope(s string) bool {
	for _, c := range scopeCatalog {
		if c.Scope == s {
			return true
		}
	}
	return false
}

// normalizeScopes de-duplicates and orders scopes in catalog order. It reports the first unknown scope.
func normalizeScopes(in []string) (out []string, unknown string) {
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !knownScope(s) {
			return nil, s
		}
		seen[s] = true
	}
	for _, c := range scopeCatalog {
		if seen[c.Scope] {
			out = append(out, c.Scope)
		}
	}
	return out, ""
}

func joinScopes(s []string) string  { return strings.Join(s, " ") }
func splitScopes(s string) []string { return strings.Fields(s) }

// intersectScopes keeps the scopes of a that are also in b, in catalog order.
func intersectScopes(a, b []string) []string {
	var out []string
	for _, c := range scopeCatalog {
		if slices.Contains(a, c.Scope) && slices.Contains(b, c.Scope) {
			out = append(out, c.Scope)
		}
	}
	return out
}
