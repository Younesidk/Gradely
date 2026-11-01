// Package engine holds the pure grading logic: the formula language and the grade / semester calculators.
// Formulas are data. They are parsed into an AST and interpreted here; no code is ever compiled or executed.
package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// FormulaError is a syntax / semantic problem found while parsing or validating a formula.
type FormulaError struct {
	Message  string `json:"message"`
	Position int    `json:"position"` // -1 when unknown
}

// ---------- AST ----------

type node interface{ pos() int }

type (
	numberNode struct {
		value float64
		at    int
	}
	variableNode struct {
		name string
		at   int
	}
	unaryNode struct {
		op      byte
		operand node
		at      int
	}
	binaryNode struct {
		op          byte
		left, right node
		at          int
	}
	callNode struct {
		name string
		args []node
		at   int
	}
)

func (n numberNode) pos() int   { return n.at }
func (n variableNode) pos() int { return n.at }
func (n unaryNode) pos() int    { return n.at }
func (n binaryNode) pos() int   { return n.at }
func (n callNode) pos() int     { return n.at }

// ---------- lexer ----------

type tokenKind int

const (
	tNumber tokenKind = iota
	tIdent
	tPlus
	tMinus
	tStar
	tSlash
	tLParen
	tRParen
	tComma
	tEnd
)

type token struct {
	kind   tokenKind
	text   string
	at     int
	number float64
}

func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
func isDigit(r rune) bool       { return r >= '0' && r <= '9' }

func tokenize(text []rune) ([]token, *FormulaError) {
	var tokens []token
	i := 0
	for i < len(text) {
		c := text[i]
		if unicode.IsSpace(c) {
			i++
			continue
		}
		if isDigit(c) || c == '.' {
			start, sawDot := i, false
			for i < len(text) && (isDigit(text[i]) || (text[i] == '.' && !sawDot)) {
				if text[i] == '.' {
					sawDot = true
				}
				i++
			}
			slice := string(text[start:i])
			n, err := strconv.ParseFloat(slice, 64)
			if slice == "." || err != nil || math.IsInf(n, 0) {
				return nil, &FormulaError{fmt.Sprintf("'%s' is not a valid number.", slice), start}
			}
			// A number directly followed by a letter ("2CC1") is almost certainly a missing operator.
			if i < len(text) && (isASCIILetter(text[i]) || text[i] == '_') {
				return nil, &FormulaError{"Missing operator: write 2 * CC1 instead of 2CC1.", i}
			}
			tokens = append(tokens, token{tNumber, slice, start, n})
			continue
		}
		if isASCIILetter(c) || c == '_' {
			start := i
			for i < len(text) && (isASCIILetter(text[i]) || text[i] == '_' || isDigit(text[i])) {
				i++
			}
			tokens = append(tokens, token{kind: tIdent, text: string(text[start:i]), at: start})
			continue
		}
		var kind tokenKind
		switch c {
		case '+':
			kind = tPlus
		case '-':
			kind = tMinus
		case '*':
			kind = tStar
		case '/':
			kind = tSlash
		case '(':
			kind = tLParen
		case ')':
			kind = tRParen
		case ',':
			kind = tComma
		default:
			return nil, &FormulaError{fmt.Sprintf("Unexpected character '%c'.", c), i}
		}
		tokens = append(tokens, token{kind: kind, text: string(c), at: i})
		i++
	}
	tokens = append(tokens, token{kind: tEnd, at: len(text)})
	return tokens, nil
}

// ---------- parser ----------

// MaxDepth caps nesting so hostile input cannot overflow the stack.
const MaxDepth = 48

type parseError struct {
	msg string
	at  int
}

type parser struct {
	tokens []token
	pos    int
	depth  int
}

// parseExpression implements:
//
//	expr    := term (('+'|'-') term)*
//	term    := unary (('*'|'/') unary)*
//	unary   := ('-'|'+') unary | primary
//	primary := NUMBER | IDENT | IDENT '(' [expr (',' expr)*] ')' | '(' expr ')'
func parseExpression(text string) (root node, ferr *FormulaError) {
	tokens, lexErr := tokenize([]rune(text))
	if lexErr != nil {
		return nil, lexErr
	}
	p := &parser{tokens: tokens}
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(parseError)
			if !ok {
				panic(r)
			}
			root, ferr = nil, &FormulaError{pe.msg, pe.at}
		}
	}()
	root = p.expression()
	if p.cur().kind != tEnd {
		if p.cur().kind == tRParen {
			p.fail("Unmatched ')'.", p.cur().at)
		}
		p.fail(fmt.Sprintf("Unexpected '%s'. Did you forget an operator?", p.cur().text), p.cur().at)
	}
	return root, nil
}

func (p *parser) fail(msg string, at int) { panic(parseError{msg, at}) }
func (p *parser) cur() token              { return p.tokens[p.pos] }
func (p *parser) advance() token          { t := p.tokens[p.pos]; p.pos++; return t }

func (p *parser) enter() {
	p.depth++
	if p.depth > MaxDepth {
		p.fail("The expression is nested too deeply.", p.cur().at)
	}
}
func (p *parser) leave() { p.depth-- }

func (p *parser) expression() node {
	p.enter()
	left := p.term()
	for p.cur().kind == tPlus || p.cur().kind == tMinus {
		op := p.advance()
		right := p.term()
		left = binaryNode{op.text[0], left, right, op.at}
	}
	p.leave()
	return left
}

func (p *parser) term() node {
	left := p.unary()
	for p.cur().kind == tStar || p.cur().kind == tSlash {
		op := p.advance()
		right := p.unary()
		left = binaryNode{op.text[0], left, right, op.at}
	}
	return left
}

func (p *parser) unary() node {
	if p.cur().kind == tPlus || p.cur().kind == tMinus {
		op := p.advance()
		p.enter()
		operand := p.unary()
		p.leave()
		return unaryNode{op.text[0], operand, op.at}
	}
	return p.primary()
}

func (p *parser) primary() node {
	t := p.cur()
	switch t.kind {
	case tNumber:
		p.advance()
		return numberNode{t.number, t.at}
	case tIdent:
		p.advance()
		if p.cur().kind == tLParen {
			p.advance()
			var args []node
			if p.cur().kind != tRParen {
				args = append(args, p.expression())
				for p.cur().kind == tComma {
					p.advance()
					args = append(args, p.expression())
				}
			}
			if p.cur().kind != tRParen {
				p.fail(fmt.Sprintf("Expected ')' to close %s(.", t.text), p.cur().at)
			}
			p.advance()
			return callNode{t.text, args, t.at}
		}
		return variableNode{t.text, t.at}
	case tLParen:
		p.advance()
		inner := p.expression()
		if p.cur().kind != tRParen {
			p.fail("Missing closing ')'.", p.cur().at)
		}
		p.advance()
		return inner
	case tEnd:
		p.fail("The expression ended unexpectedly.", t.at)
	}
	p.fail(fmt.Sprintf("Unexpected '%s'.", t.text), t.at)
	return nil
}

// ---------- variables & functions ----------

const (
	VarCC1  = "CC1"
	VarCC2  = "CC2"
	VarLab  = "Lab"
	VarExam = "Exam"
)

var (
	AllVariables   = []string{VarCC1, VarCC2, VarLab, VarExam}
	NoLabVariables = []string{VarCC1, VarCC2, VarExam}

	functionSignatures = []FunctionSignature{
		{"AVG", 1, math.MaxInt32, "Average of its arguments"},
		{"MIN", 1, math.MaxInt32, "Smallest argument"},
		{"MAX", 1, math.MaxInt32, "Largest argument"},
		{"ABS", 1, 1, "Absolute value"},
		{"ROUND", 1, 2, "Round to n decimals (default 0)"},
	}
)

// FunctionSignature describes a function a formula may call.
type FunctionSignature struct {
	Name        string `json:"name"`
	MinArgs     int    `json:"minArgs"`
	MaxArgs     int    `json:"maxArgs"`
	Description string `json:"description"`
}

// Functions lists the available formula functions.
func Functions() []FunctionSignature { return functionSignatures }

// VariablesFor lists the variables a module of the given kind can use.
func VariablesFor(hasLab bool) []string {
	if hasLab {
		return AllVariables
	}
	return NoLabVariables
}

// CanonicalVariable returns the correctly cased variable name, or "" when unknown.
func CanonicalVariable(name string) string {
	for _, v := range AllVariables {
		if strings.EqualFold(v, name) {
			return v
		}
	}
	return ""
}

func findFunction(name string) *FunctionSignature {
	for i := range functionSignatures {
		if strings.EqualFold(functionSignatures[i].Name, name) {
			return &functionSignatures[i]
		}
	}
	return nil
}
