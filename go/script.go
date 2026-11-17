package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// A tiny calculator language: numbers only, no files, no network, no system access, no strings, no functions
// defined by the user. Every run is capped (steps, loops, output, source length). It is NOT Python or a shell.
//
//   let x = 5; for i = 1 to 10 { let x = x + i }; print x
//   if x > 3 { print 1 } else { print 0 }

const (
	scriptMaxSrc   = 800
	scriptMaxSteps = 200000
	scriptMaxLines = 40
	scriptMaxLoop  = 100000
	scriptMaxDepth = 6
)

var reScript = regexp.MustCompile(`(?is)^\s*(?:run\s+)?(?:a\s+)?(?:calc(?:ulator)?\s+script|calc\s+script)\s*[:\-]\s*(.+)$`)

type sTok struct {
	k string // num id op kw eof
	s string
	n float64
}

type sInterp struct {
	toks  []sTok
	p     int
	vars  map[string]float64
	steps int
	out   []string
	depth int
	err   error
}

type scriptErr struct{ msg string }

func (e scriptErr) Error() string { return e.msg }

func lexScript(src string) ([]sTok, error) {
	var out []sTok
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case unicode.IsDigit(c) || (c == '.' && i+1 < len(rs) && unicode.IsDigit(rs[i+1])):
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.') {
				j++
			}
			v, err := strconv.ParseFloat(string(rs[i:j]), 64)
			if err != nil {
				return nil, scriptErr{"bad number " + string(rs[i:j])}
			}
			out = append(out, sTok{k: "num", n: v})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_') {
				j++
			}
			w := strings.ToLower(string(rs[i:j]))
			switch w {
			case "let", "for", "to", "if", "else", "print", "while":
				out = append(out, sTok{k: "kw", s: w})
			default:
				out = append(out, sTok{k: "id", s: w})
			}
			i = j
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||":
				out = append(out, sTok{k: "op", s: two})
				i += 2
				continue
			}
			if strings.ContainsRune("+-*/%^()<>=;{},!", c) {
				out = append(out, sTok{k: "op", s: string(c)})
				i++
				continue
			}
			return nil, scriptErr{"unsupported character " + strconv.Quote(string(c))}
		}
	}
	return append(out, sTok{k: "eof"}), nil
}

func (s *sInterp) fail(m string) { panic(scriptErr{m}) }
func (s *sInterp) cur() sTok     { return s.toks[s.p] }
func (s *sInterp) isOp(o string) bool {
	return s.cur().k == "op" && s.cur().s == o
}
func (s *sInterp) isKw(o string) bool { return s.cur().k == "kw" && s.cur().s == o }
func (s *sInterp) want(o string) {
	if !s.isOp(o) {
		s.fail("expected " + o)
	}
	s.p++
}
func (s *sInterp) tick() {
	s.steps++
	if s.steps > scriptMaxSteps {
		s.fail("stopped: too many steps (limit " + strconv.Itoa(scriptMaxSteps) + ")")
	}
}

// block parses and, when run is true, executes statements until "}" or eof. When run is false it only skips.
func (s *sInterp) block(run bool) {
	for !s.isOp("}") && s.cur().k != "eof" {
		if s.isOp(";") {
			s.p++
			continue
		}
		s.stmt(run)
	}
}

func (s *sInterp) braces(run bool) {
	s.want("{")
	s.depth++
	if s.depth > scriptMaxDepth {
		s.fail("nesting too deep")
	}
	s.block(run)
	s.depth--
	s.want("}")
}

func (s *sInterp) stmt(run bool) {
	s.tick()
	t := s.cur()
	switch {
	case t.k == "kw" && t.s == "let", t.k == "id" && s.toks[s.p+1].k == "op" && s.toks[s.p+1].s == "=":
		if t.k == "kw" {
			s.p++
		}
		id := s.cur()
		if id.k != "id" {
			s.fail("expected a name after let")
		}
		s.p++
		s.want("=")
		v := s.expr()
		if run {
			if len(s.vars) >= 50 {
				if _, ok := s.vars[id.s]; !ok {
					s.fail("too many variables (limit 50)")
				}
			}
			s.vars[id.s] = v
		}
	case t.k == "kw" && t.s == "print":
		s.p++
		v := s.expr()
		if run {
			if len(s.out) >= scriptMaxLines {
				s.fail("stopped: too many output lines (limit " + strconv.Itoa(scriptMaxLines) + ")")
			}
			s.out = append(s.out, fnum(v))
		}
	case t.k == "kw" && t.s == "for":
		s.p++
		id := s.cur()
		if id.k != "id" {
			s.fail("expected a loop name")
		}
		s.p++
		s.want("=")
		a := s.expr()
		if !s.isKw("to") {
			s.fail("expected to")
		}
		s.p++
		b := s.expr()
		start := s.p
		if !run {
			s.braces(false)
			return
		}
		if b-a > scriptMaxLoop {
			s.fail("loop too long (limit " + strconv.Itoa(scriptMaxLoop) + ")")
		}
		if a > b {
			s.braces(false)
			return
		}
		for i := a; i <= b; i++ {
			s.vars[id.s] = i
			s.p = start
			s.braces(true)
		}
	case t.k == "kw" && t.s == "while":
		s.p++
		condPos := s.p
		if !run {
			s.expr()
			s.braces(false)
			return
		}
		for n := 0; ; n++ {
			if n > scriptMaxLoop {
				s.fail("loop too long (limit " + strconv.Itoa(scriptMaxLoop) + ")")
			}
			s.p = condPos
			c := s.expr()
			if c == 0 {
				s.braces(false)
				return
			}
			s.braces(true)
		}
	case t.k == "kw" && t.s == "if":
		s.p++
		c := s.expr()
		s.braces(run && c != 0)
		if s.isKw("else") {
			s.p++
			s.braces(run && c == 0)
		}
	default:
		s.fail("I don't understand \"" + tokText(t) + "\" here")
	}
}

func tokText(t sTok) string {
	switch t.k {
	case "num":
		return fnum(t.n)
	case "eof":
		return "the end"
	}
	return t.s
}

func (s *sInterp) expr() float64 { return s.or() }
func (s *sInterp) or() float64 {
	v := s.and()
	for s.isOp("||") {
		s.p++
		w := s.and()
		v = b2f(v != 0 || w != 0)
	}
	return v
}
func (s *sInterp) and() float64 {
	v := s.cmp()
	for s.isOp("&&") {
		s.p++
		w := s.cmp()
		v = b2f(v != 0 && w != 0)
	}
	return v
}
func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
func (s *sInterp) cmp() float64 {
	v := s.add()
	for {
		t := s.cur()
		if t.k != "op" {
			return v
		}
		switch t.s {
		case "==", "!=", "<", ">", "<=", ">=":
			s.p++
			w := s.add()
			switch t.s {
			case "==":
				v = b2f(v == w)
			case "!=":
				v = b2f(v != w)
			case "<":
				v = b2f(v < w)
			case ">":
				v = b2f(v > w)
			case "<=":
				v = b2f(v <= w)
			case ">=":
				v = b2f(v >= w)
			}
		default:
			return v
		}
	}
}
func (s *sInterp) add() float64 {
	v := s.mul()
	for s.isOp("+") || s.isOp("-") {
		op := s.cur().s
		s.p++
		w := s.mul()
		if op == "+" {
			v += w
		} else {
			v -= w
		}
	}
	return v
}
func (s *sInterp) mul() float64 {
	v := s.pow()
	for s.isOp("*") || s.isOp("/") || s.isOp("%") {
		op := s.cur().s
		s.p++
		w := s.pow()
		switch op {
		case "*":
			v *= w
		case "/":
			if w == 0 {
				s.fail("division by zero")
			}
			v /= w
		case "%":
			if w == 0 {
				s.fail("division by zero")
			}
			v = math.Mod(v, w)
		}
	}
	return v
}
func (s *sInterp) pow() float64 {
	v := s.unary()
	if s.isOp("^") {
		s.p++
		w := s.pow()
		if math.Abs(w) > 1000 {
			s.fail("exponent too large")
		}
		v = math.Pow(v, w)
		if math.IsInf(v, 0) || math.IsNaN(v) {
			s.fail("result is not a finite number")
		}
	}
	return v
}
func (s *sInterp) unary() float64 {
	if s.isOp("-") {
		s.p++
		return -s.unary()
	}
	if s.isOp("!") {
		s.p++
		return b2f(s.unary() == 0)
	}
	return s.atom()
}

var scriptFuncs = map[string]func(a []float64) (float64, bool){
	"sqrt":  func(a []float64) (float64, bool) { return math.Sqrt(a[0]), len(a) == 1 && a[0] >= 0 },
	"abs":   func(a []float64) (float64, bool) { return math.Abs(a[0]), len(a) == 1 },
	"round": func(a []float64) (float64, bool) { return math.Round(a[0]), len(a) == 1 },
	"floor": func(a []float64) (float64, bool) { return math.Floor(a[0]), len(a) == 1 },
	"ceil":  func(a []float64) (float64, bool) { return math.Ceil(a[0]), len(a) == 1 },
	"sin":   func(a []float64) (float64, bool) { return math.Sin(a[0]), len(a) == 1 },
	"cos":   func(a []float64) (float64, bool) { return math.Cos(a[0]), len(a) == 1 },
	"ln":    func(a []float64) (float64, bool) { return math.Log(a[0]), len(a) == 1 && a[0] > 0 },
	"log10": func(a []float64) (float64, bool) { return math.Log10(a[0]), len(a) == 1 && a[0] > 0 },
	"min":   func(a []float64) (float64, bool) { return math.Min(a[0], a[1]), len(a) == 2 },
	"max":   func(a []float64) (float64, bool) { return math.Max(a[0], a[1]), len(a) == 2 },
}

func (s *sInterp) atom() float64 {
	s.tick()
	t := s.cur()
	switch {
	case t.k == "num":
		s.p++
		return t.n
	case t.k == "op" && t.s == "(":
		s.p++
		v := s.expr()
		s.want(")")
		return v
	case t.k == "id":
		s.p++
		if s.isOp("(") {
			f, ok := scriptFuncs[t.s]
			if !ok {
				s.fail("unknown function " + t.s)
			}
			s.p++
			var args []float64
			for !s.isOp(")") {
				args = append(args, s.expr())
				if s.isOp(",") {
					s.p++
				} else {
					break
				}
			}
			s.want(")")
			if len(args) == 0 {
				s.fail(t.s + " needs a number")
			}
			v, ok := f(args)
			if !ok {
				s.fail("bad input for " + t.s)
			}
			return v
		}
		switch t.s {
		case "pi":
			return math.Pi
		case "e":
			return math.E
		}
		v, ok := s.vars[t.s]
		if !ok {
			s.fail("unknown name " + t.s + " (use let " + t.s + " = ... first)")
		}
		return v
	}
	s.fail("I don't understand \"" + tokText(t) + "\" here")
	return 0
}

// RunScript runs a calculator script. ok=false means the text is not a script request.
func RunScript(text string) (string, bool) {
	m := reScript.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	src := strings.TrimSpace(m[1])
	if len([]rune(src)) > scriptMaxSrc {
		return fmt.Sprintf("That script is too long (limit %d characters).", scriptMaxSrc), true
	}
	toks, err := lexScript(src)
	if err != nil {
		return "Script error: " + err.Error(), true
	}
	s := &sInterp{toks: toks, vars: map[string]float64{}}
	var res string
	func() {
		defer func() {
			if r := recover(); r != nil {
				if se, ok := r.(scriptErr); ok {
					res = "Script error: " + se.msg
					return
				}
				res = "Script error: could not run that."
			}
		}()
		s.block(true)
		if s.cur().k != "eof" {
			s.fail("unexpected }")
		}
	}()
	if res != "" {
		if len(s.out) > 0 {
			res = strings.Join(s.out, "\n") + "\n" + res
		}
		return res, true
	}
	if len(s.out) == 0 {
		return "The script ran and printed nothing. Add: print <expression>", true
	}
	return strings.Join(s.out, "\n"), true
}
