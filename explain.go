package govaluate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

/*
ExplainMode controls how much of an expression an explaining evaluation walks.
*/
type ExplainMode int

const (
	/*
		ExplainFirst keeps the short-circuiting behaviour of a normal evaluation, so a conjunction
		reports only its first failing term. This is the cheapest mode, but it hides every cause
		after the first one.
	*/
	ExplainFirst ExplainMode = iota

	/*
		ExplainAll evaluates branches that a short-circuit would normally skip, so that every
		failing term of a conjunction is reported. The returned value is unaffected: a branch that
		only ran for the sake of the explanation cannot change the result, and an error it raises is
		recorded on its node instead of being returned.

		This mode evaluates the entire tree, which costs the same as evaluating an expression that
		matches. It also runs functions that a normal evaluation would have skipped, so it should
		only be used with side-effect free functions.
	*/
	ExplainAll
)

// maxRenderedListItems is the point past which a literal list is summarized rather than printed.
const maxRenderedListItems = 8

/*
Explanation is the record of an explaining evaluation: the whole evaluated tree, plus the
terms that account for a false result.
*/
type Explanation struct {

	// Expression is the source expression, as given to the parser.
	Expression string

	// Mode is the mode the explaining evaluation ran in.
	Mode ExplainMode

	// Result is the value the expression evaluated to. It is identical to what `Eval` returns for
	// the same parameters.
	Result interface{}

	// Root is the top of the evaluated tree. It is nil only for an empty expression.
	Root *ExplainNode
}

/*
ExplainNode is a single evaluated node of the expression tree.
*/
type ExplainNode struct {

	// Text is this sub-expression rendered back to a string. It is a canonical rendering rather
	// than the original source: operators use their canonical symbol (`=` for `==`) and long
	// literal lists are summarized as `[...N values]`.
	Text string

	// Symbol is the operator this node applies. Leaf nodes carry VALUE (a parameter) or LITERAL.
	Symbol OperatorSymbol

	// Function is the name of the called function, for FUNCTIONAL nodes only.
	Function string

	// Result is the value this node evaluated to, or nil if it errored.
	Result interface{}

	// Err is the error this node raised, if any.
	Err error

	// Skipped marks a node that a normal evaluation would not have reached, because a
	// short-circuit had already decided the result. Only ExplainAll produces these.
	Skipped bool

	// Vars holds every parameter referenced in this subtree, with the value it resolved to.
	// Leaf nodes carry theirs from the start; on any other node it is filled in only when the node
	// turns out to be a failing term, since collecting them for the whole tree is wasted work.
	Vars map[string]interface{}

	// Children are the operands of this node, left before right.
	Children []*ExplainNode
}

/*
Passed reports whether this node evaluated to exactly true.
*/
func (this *ExplainNode) Passed() bool {
	return this != nil && this.Err == nil && this.Result == true
}

/*
FailedTerms returns the smallest set of sub-expressions that account for a false result.

Conjunctions are descended into, so a top-level chain of `&&` reports each of its failing terms
individually. Anything else - a comparison, a function call, a negation, a disjunction - is
reported whole, since no part of it fails on its own.

Returns nil when the expression passed.
*/
func (this *Explanation) FailedTerms() []*ExplainNode {

	if this == nil || this.Root == nil {
		return nil
	}

	var terms []*ExplainNode
	collectFailedTerms(this.Root, &terms)
	return terms
}

/*
FailedVars returns the parameters referenced by the failing terms, with the values they
resolved to. It is the flattened form of FailedTerms, for callers that only need to know which
attributes caused the failure.
*/
func (this *Explanation) FailedVars() map[string]interface{} {

	terms := this.FailedTerms()
	if len(terms) == 0 {
		return nil
	}

	vars := make(map[string]interface{})
	for _, term := range terms {
		for name, value := range term.Vars {
			vars[name] = value
		}
	}

	if len(vars) == 0 {
		return nil
	}
	return vars
}

/*
String renders the failing terms of the explanation, one per line.
*/
func (this *Explanation) String() string {

	if this == nil {
		return ""
	}

	terms := this.FailedTerms()
	if len(terms) == 0 {
		return fmt.Sprintf("%v", this.Result)
	}

	lines := make([]string, 0, len(terms))
	for _, term := range terms {
		line := term.Text
		if term.Err != nil {
			line += fmt.Sprintf(" [error: %s]", term.Err.Error())
		}
		if len(term.Vars) > 0 {
			lines = append(lines, line+" -> "+renderVars(term.Vars))
			continue
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func collectFailedTerms(node *ExplainNode, terms *[]*ExplainNode) {

	if node == nil || node.Passed() {
		return
	}

	switch node.Symbol {
	case NOOP:
		// a parenthesized sub-expression explains nothing on its own
		if len(node.Children) == 1 {
			collectFailedTerms(node.Children[0], terms)
			return
		}
	case AND:
		// every operand must hold, so each failing one is a cause in its own right
		descended := false
		for _, child := range node.Children {
			if !child.Passed() {
				collectFailedTerms(child, terms)
				descended = true
			}
		}
		if descended {
			return
		}
	}

	// this node is being reported, so it needs the parameters of its whole subtree
	node.collectVars()

	*terms = append(*terms, node)
}

/*
EvaluateWithExplanation is the same as EvalWithExplanation, but wraps a plain map of parameters.
*/
func (this EvaluableExpression) EvaluateWithExplanation(parameters map[string]interface{}, mode ExplainMode) (interface{}, *Explanation, error) {

	if parameters == nil {
		return this.EvalWithExplanation(nil, mode)
	}

	return this.EvalWithExplanation(MapParameters(parameters), mode)
}

/*
EvalWithExplanation runs the expression like Eval does, and additionally returns a record of how
each part of it evaluated. The returned value and error are the same ones Eval would return for
the same parameters.

An explanation is always returned, even alongside an error, so that the caller can see how far
evaluation got.
*/
func (this EvaluableExpression) EvalWithExplanation(parameters Parameters, mode ExplainMode) (interface{}, *Explanation, error) {

	explanation := &Explanation{
		Expression: this.inputExpression,
		Mode:       mode,
	}

	if this.evaluationStages == nil {
		return nil, explanation, nil
	}

	if parameters != nil {
		parameters = &sanitizedParameters{parameters}
	} else {
		parameters = DUMMY_PARAMETERS
	}

	result, node, err := this.explainStage(this.evaluationStages, parameters, mode, false)

	explanation.Root = node
	if err != nil {
		return nil, explanation, err
	}

	explanation.Result = result
	return result, explanation, nil
}

/*
explainStage mirrors evaluateStage, recording every node as it goes.

[skipped] marks a subtree that a short-circuit has already made irrelevant. Such a subtree is
still evaluated in ExplainAll mode, but neither its value nor its errors may escape.
*/
func (this EvaluableExpression) explainStage(
	stage *evaluationStage,
	parameters Parameters,
	mode ExplainMode,
	skipped bool) (interface{}, *ExplainNode, error) {

	var left, right interface{}
	var leftNode, rightNode *ExplainNode
	var err error

	node := &ExplainNode{
		Text:     stage.text,
		Symbol:   stage.symbol,
		Function: stage.functionName,
		Skipped:  skipped,
	}

	if stage.leftStage != nil {

		left, leftNode, err = this.explainStage(stage.leftStage, parameters, mode, skipped)
		node.addChild(leftNode)

		if err != nil {
			return nil, node.fail(err), passUp(err, skipped)
		}
	}

	// a short-circuit decides the result here, without the right-hand side
	shortCircuited := false
	var shortCircuitResult interface{}

	if stage.isShortCircuitable() {
		switch stage.symbol {
		case AND:
			if left == false {
				shortCircuited, shortCircuitResult = true, false
			}
		case OR:
			if left == true {
				shortCircuited, shortCircuitResult = true, true
			}
		case COALESCE:
			if left != nil {
				shortCircuited, shortCircuitResult = true, left
			}
		case TERNARY_TRUE:
			if left == false {
				right = shortCircuitHolder
			}
		case TERNARY_FALSE:
			if left != nil {
				right = shortCircuitHolder
			}
		}
	}

	if shortCircuited && mode != ExplainAll {
		node.Result = shortCircuitResult
		return shortCircuitResult, node, nil
	}

	if right != shortCircuitHolder && stage.rightStage != nil {

		// past a short-circuit the right-hand side is evaluated for the explanation only
		rightSkipped := skipped || shortCircuited

		right, rightNode, err = this.explainStage(stage.rightStage, parameters, mode, rightSkipped)
		node.addChild(rightNode)

		if err != nil {
			return nil, node.fail(err), passUp(err, skipped)
		}
	}

	if shortCircuited {
		node.Result = shortCircuitResult
		return shortCircuitResult, node, nil
	}

	if this.ChecksTypes {
		if stage.typeCheck == nil {

			err = typeCheck(stage.leftTypeCheck, left, stage.symbol, stage.typeErrorFormat)
			if err != nil {
				return nil, node.fail(err), passUp(err, skipped)
			}

			err = typeCheck(stage.rightTypeCheck, right, stage.symbol, stage.typeErrorFormat)
			if err != nil {
				return nil, node.fail(err), passUp(err, skipped)
			}
		} else {
			// special case where the type check needs to know both sides to determine if the operator can handle it
			if !stage.typeCheck(left, right) {
				err = fmt.Errorf(stage.typeErrorFormat, left, stage.symbol.String())
				return nil, node.fail(err), passUp(err, skipped)
			}
		}
	}

	result, err := stage.operator(left, right, parameters)
	if err != nil {
		return nil, node.fail(err), passUp(err, skipped)
	}

	node.Result = result

	if stage.parameterName != "" {
		node.Vars = map[string]interface{}{stage.parameterName: result}
	}

	return result, node, nil
}

// passUp reports an error to the caller unless the failing subtree was only evaluated to explain
// the result, in which case the error stays on its node.
func passUp(err error, skipped bool) error {

	if skipped {
		return nil
	}
	return err
}

func (this *ExplainNode) fail(err error) *ExplainNode {

	this.Err = err
	return this
}

func (this *ExplainNode) addChild(child *ExplainNode) {

	if child == nil {
		return
	}

	if this.Children == nil {
		this.Children = make([]*ExplainNode, 0, 2)
	}

	this.Children = append(this.Children, child)
}

// collectVars gathers the parameters of this node's subtree onto the node itself. Leaf nodes
// already carry theirs, and a node that has already been collected is left alone.
func (this *ExplainNode) collectVars() {

	if this.Vars != nil || len(this.Children) == 0 {
		return
	}

	vars := make(map[string]interface{})

	var walk func(node *ExplainNode)
	walk = func(node *ExplainNode) {
		for name, value := range node.Vars {
			vars[name] = value
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(this)

	if len(vars) > 0 {
		this.Vars = vars
	}
}

/*
annotateStages renders every stage of a planned tree back to a string, once, so that an
explaining evaluation does not have to build them.
*/
func annotateStages(stage *evaluationStage) string {

	if stage == nil {
		return ""
	}

	left := annotateStages(stage.leftStage)
	right := annotateStages(stage.rightStage)

	stage.text = renderStage(stage, left, right)
	return stage.text
}

func renderStage(stage *evaluationStage, left string, right string) string {

	switch stage.symbol {
	case VALUE:
		return stage.parameterName
	case LITERAL:
		return renderLiteral(stage.literal)
	case NOOP:
		return "(" + right + ")"
	case FUNCTIONAL:
		return stage.functionName + renderArguments(stage, right)
	case ACCESS:
		name := strings.Join(stage.accessorPath, ".")
		if stage.rightStage == nil {
			return name
		}
		return name + renderArguments(stage, right)
	case SEPARATE:
		return left + ", " + right
	case NEGATE, INVERT, BITWISE_NOT:
		return stage.symbol.String() + right
	}

	if left == "" {
		return strings.TrimSpace(stage.symbol.String() + " " + right)
	}

	return left + " " + stage.symbol.String() + " " + right
}

// renderArguments renders a call's argument list. The arguments are parsed as a parenthesized
// clause, which already renders with its own parentheses.
func renderArguments(stage *evaluationStage, right string) string {

	if stage.rightStage == nil {
		return "()"
	}
	if stage.rightStage.symbol == NOOP {
		return right
	}
	return "(" + right + ")"
}

func renderLiteral(value interface{}) string {

	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(v)
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case *regexp.Regexp:
		return strconv.Quote(v.String())
	case map[interface{}]struct{}:
		// `in` lists are condensed into a set at parse time, and are routinely hundreds long
		return fmt.Sprintf("[...%d values]", len(v))
	case []interface{}:
		if len(v) > maxRenderedListItems {
			return fmt.Sprintf("[...%d values]", len(v))
		}
		items := make([]string, len(v))
		for i := range v {
			items[i] = renderLiteral(v[i])
		}
		return "[" + strings.Join(items, ", ") + "]"
	}

	return fmt.Sprintf("%v", value)
}

func renderVars(vars map[string]interface{}) string {

	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)

	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+" = "+renderLiteral(vars[name]))
	}

	return strings.Join(pairs, ", ")
}
