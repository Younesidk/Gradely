package engine

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// MaxFormulaLength is the longest accepted formula text.
const MaxFormulaLength = 500

// ErrEvaluation is returned when a parsed formula cannot be evaluated for the supplied values.
type ErrEvaluation struct{ Message string }

func (e *ErrEvaluation) Error() string { return e.Message }

// ErrNotLinear is returned by Linearize when the formula is not a linear function of the unknown variable.
type ErrNotLinear struct{ Message string }

func (e *ErrNotLinear) Error() string { return e.Message }

// IsNotLinear reports whether err is an *ErrNotLinear.
func IsNotLinear(err error) bool { var e *ErrNotLinear; return errors.As(err, &e) }

// Formula is a parsed, validated grading formula.
type Formula struct {
	Text      string
	Variables map[string]bool // canonical names referenced by the formula
	root      node
}

// ParseResult is the outcome of Parse.
type ParseResult struct {
	Formula *Formula
	Errors  []FormulaError
}

func (r ParseResult) Success() bool { return r.Formula != nil && len(r.Errors) == 0 }

func (r ParseResult) FirstError() string {
	if len(r.Errors) == 0 {
		return ""
	}
	return r.Errors[0].Message
}

func fail(msg string, pos int) ParseResult {
	return ParseResult{Errors: []FormulaError{{msg, pos}}}
}

// Parse checks text and the variables / functions it uses. It never panics on bad user input.
// allowLab says whether the Lab variable is available (the module has a lab exam).
func Parse(text string, allowLab bool) ParseResult {
	text = strings.TrimSpace(text)
	if text == "" {
		return fail("Enter a formula, for example (CC1 + CC2) / 2 * 0.4 + Exam * 0.6.", -1)
	}
	if len([]rune(text)) > MaxFormulaLength {
		return fail(fmt.Sprintf("The formula is too long (maximum %d characters).", MaxFormulaLength), -1)
	}
	root, perr := parseExpression(text)
	if perr != nil {
		return ParseResult{Errors: []FormulaError{*perr}}
	}
	vars := map[string]bool{}
	var errs []FormulaError
	validate(root, allowLab, vars, &errs)
	if len(errs) > 0 {
		return ParseResult{Errors: errs}
	}
	return ParseResult{Formula: &Formula{Text: text, Variables: vars, root: root}}
}

func validate(n node, allowLab bool, vars map[string]bool, errs *[]FormulaError) {
	switch v := n.(type) {
	case variableNode:
		canonical := CanonicalVariable(v.name)
		switch {
		case canonical == "":
			*errs = append(*errs, FormulaError{fmt.Sprintf("Unknown variable '%s'. Use CC1, CC2, Lab or Exam.", v.name), v.at})
		case canonical == VarLab && !allowLab:
			*errs = append(*errs, FormulaError{"Lab is not available: this formula is for modules without a lab exam.", v.at})
		default:
			vars[canonical] = true
		}
	case unaryNode:
		validate(v.operand, allowLab, vars, errs)
	case binaryNode:
		validate(v.left, allowLab, vars, errs)
		validate(v.right, allowLab, vars, errs)
	case callNode:
		sig := findFunction(v.name)
		switch {
		case sig == nil:
			*errs = append(*errs, FormulaError{fmt.Sprintf("Unknown function '%s'. Available: AVG, MIN, MAX, ABS, ROUND.", v.name), v.at})
		case len(v.args) < sig.MinArgs || len(v.args) > sig.MaxArgs:
			var msg string
			switch {
			case sig.MinArgs == sig.MaxArgs:
				msg = fmt.Sprintf("%s() takes exactly %d argument(s).", sig.Name, sig.MinArgs)
			case sig.MaxArgs == math.MaxInt32:
				msg = fmt.Sprintf("%s() takes %d or more arguments.", sig.Name, sig.MinArgs)
			default:
				msg = fmt.Sprintf("%s() takes %d to %d arguments.", sig.Name, sig.MinArgs, sig.MaxArgs)
			}
			*errs = append(*errs, FormulaError{msg, v.at})
		}
		for _, a := range v.args {
			validate(a, allowLab, vars, errs)
		}
	}
}

// linear is a value of the form A + B·x where x is the single unknown variable (if any).
type linear struct{ a, b float64 }

func (l linear) dependsOnUnknown() bool { return l.b != 0 }

func finite(d float64) (float64, error) {
	if math.IsInf(d, 0) || math.IsNaN(d) {
		return 0, &ErrEvaluation{"The result is not a finite number."}
	}
	return d, nil
}

// Evaluate computes the formula. It returns *ErrEvaluation on missing values or invalid math.
func (f *Formula) Evaluate(values map[string]float64) (float64, error) {
	l, err := eval(f.root, values, "")
	if err != nil {
		return 0, err
	}
	return finite(l.a)
}

// Linearize expresses the formula as A + B·unknown with every other variable taken from values.
// It returns *ErrNotLinear if the formula is not provably linear in the unknown (MAX(Exam, 10), Exam * Exam,
// 1 / Exam, ...) so callers never fabricate an answer.
func (f *Formula) Linearize(unknown string, values map[string]float64) (a, b float64, err error) {
	l, err := eval(f.root, values, unknown)
	if err != nil {
		return 0, 0, err
	}
	if a, err = finite(l.a); err != nil {
		return 0, 0, err
	}
	if b, err = finite(l.b); err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

func eval(n node, values map[string]float64, unknown string) (linear, error) {
	switch v := n.(type) {
	case numberNode:
		return linear{v.value, 0}, nil

	case variableNode:
		name := CanonicalVariable(v.name)
		if name == "" {
			return linear{}, &ErrEvaluation{fmt.Sprintf("Unknown variable '%s'.", v.name)}
		}
		if unknown != "" && name == unknown {
			return linear{0, 1}, nil
		}
		val, ok := values[name]
		if !ok {
			return linear{}, &ErrEvaluation{fmt.Sprintf("No value for %s.", name)}
		}
		return linear{val, 0}, nil

	case unaryNode:
		o, err := eval(v.operand, values, unknown)
		if err != nil {
			return linear{}, err
		}
		if v.op == '-' {
			return linear{-o.a, -o.b}, nil
		}
		return o, nil

	case binaryNode:
		l, err := eval(v.left, values, unknown)
		if err != nil {
			return linear{}, err
		}
		r, err := eval(v.right, values, unknown)
		if err != nil {
			return linear{}, err
		}
		switch v.op {
		case '+':
			return linear{l.a + r.a, l.b + r.b}, nil
		case '-':
			return linear{l.a - r.a, l.b - r.b}, nil
		case '*':
			if l.dependsOnUnknown() && r.dependsOnUnknown() {
				return linear{}, &ErrNotLinear{"The unknown variable is multiplied by itself."}
			}
			if l.dependsOnUnknown() {
				return linear{l.a * r.a, l.b * r.a}, nil
			}
			return linear{l.a * r.a, l.a * r.b}, nil
		case '/':
			if r.dependsOnUnknown() {
				return linear{}, &ErrNotLinear{"The unknown variable appears in a denominator."}
			}
			if r.a == 0 {
				return linear{}, &ErrEvaluation{"Division by zero."}
			}
			return linear{l.a / r.a, l.b / r.a}, nil
		}
		return linear{}, &ErrEvaluation{fmt.Sprintf("Unsupported operator '%c'.", v.op)}

	case callNode:
		args := make([]linear, len(v.args))
		for i, a := range v.args {
			l, err := eval(a, values, unknown)
			if err != nil {
				return linear{}, err
			}
			args[i] = l
		}
		name := strings.ToUpper(v.name)
		if name == "AVG" {
			var a, b float64
			for _, x := range args {
				a += x.a
				b += x.b
			}
			return linear{a / float64(len(args)), b / float64(len(args))}, nil
		}
		for _, x := range args {
			if x.dependsOnUnknown() {
				return linear{}, &ErrNotLinear{fmt.Sprintf("%s() is applied to the unknown variable.", name)}
			}
		}
		switch name {
		case "MIN":
			m := args[0].a
			for _, x := range args[1:] {
				m = math.Min(m, x.a)
			}
			return linear{m, 0}, nil
		case "MAX":
			m := args[0].a
			for _, x := range args[1:] {
				m = math.Max(m, x.a)
			}
			return linear{m, 0}, nil
		case "ABS":
			return linear{math.Abs(args[0].a), 0}, nil
		case "ROUND":
			digits := 0.0
			if len(args) > 1 {
				digits = args[1].a
			}
			return linear{roundTo(args[0].a, digits), 0}, nil
		}
		return linear{}, &ErrEvaluation{fmt.Sprintf("Unknown function '%s'.", v.name)}
	}
	return linear{}, &ErrEvaluation{"Unsupported expression."}
}

// roundTo rounds half away from zero to the given number of decimals (clamped to 0..10).
func roundTo(value, digits float64) float64 {
	d := math.Max(0, math.Min(10, math.Round(digits)))
	p := math.Pow(10, d)
	return math.Round(value*p) / p
}
