package govaluate

import (
	"fmt"
	"strings"
	"testing"
)

func mustExpression(t *testing.T, expression string) *EvaluableExpression {

	t.Helper()

	parsed, err := NewEvaluableExpression(expression)
	if err != nil {
		t.Fatalf("unable to parse '%s': %v", expression, err)
	}
	return parsed
}

func failedTermTexts(explanation *Explanation) []string {

	terms := explanation.FailedTerms()
	texts := make([]string, len(terms))
	for i, term := range terms {
		texts[i] = term.Text
	}
	return texts
}

/*
	The default mode keeps short-circuiting, so only the first failing term of a conjunction shows up.
*/
func TestExplainFirstStopsAtFirstFailure(t *testing.T) {

	expression := mustExpression(t, "a > 1 && b > 2 && c > 3")
	parameters := map[string]interface{}{"a": 5, "b": 0, "c": 0}

	result, explanation, err := expression.EvaluateWithExplanation(parameters, ExplainFirst)
	if err != nil {
		t.Fatal(err)
	}
	if result != false {
		t.Fatalf("expected false, got %v", result)
	}

	texts := failedTermTexts(explanation)
	if len(texts) != 1 || texts[0] != "b > 2" {
		t.Fatalf("expected only 'b > 2' to be reported, got %v", texts)
	}
}

/*
	ExplainAll walks past the short-circuit, so every failing term is reported.
*/
func TestExplainAllReportsEveryFailure(t *testing.T) {

	expression := mustExpression(t, "a > 1 && b > 2 && c > 3")
	parameters := map[string]interface{}{"a": 5, "b": 0, "c": 0}

	result, explanation, err := expression.EvaluateWithExplanation(parameters, ExplainAll)
	if err != nil {
		t.Fatal(err)
	}
	if result != false {
		t.Fatalf("expected false, got %v", result)
	}

	texts := failedTermTexts(explanation)
	if len(texts) != 2 || texts[0] != "b > 2" || texts[1] != "c > 3" {
		t.Fatalf("expected 'b > 2' and 'c > 3' to be reported, got %v", texts)
	}
}

/*
	The failing terms carry the parameters they read, and the values those resolved to.
*/
func TestExplainReportsFailingVars(t *testing.T) {

	expression := mustExpression(t, "zipcode in ('33012', '33013') && age >= 25")
	parameters := map[string]interface{}{"zipcode": "90210", "age": 19}

	_, explanation, err := expression.EvaluateWithExplanation(parameters, ExplainAll)
	if err != nil {
		t.Fatal(err)
	}

	vars := explanation.FailedVars()
	if len(vars) != 2 {
		t.Fatalf("expected two failing vars, got %v", vars)
	}
	if vars["zipcode"] != "90210" {
		t.Errorf("expected zipcode = 90210, got %v", vars["zipcode"])
	}
	if vars["age"] != float64(19) {
		t.Errorf("expected age = 19, got %v", vars["age"])
	}
}

func TestExplainPassingExpressionHasNoFailures(t *testing.T) {

	expression := mustExpression(t, "a > 1 && b > 2")
	parameters := map[string]interface{}{"a": 5, "b": 5}

	result, explanation, err := expression.EvaluateWithExplanation(parameters, ExplainAll)
	if err != nil {
		t.Fatal(err)
	}
	if result != true {
		t.Fatalf("expected true, got %v", result)
	}
	if terms := explanation.FailedTerms(); len(terms) != 0 {
		t.Fatalf("expected no failing terms, got %v", failedTermTexts(explanation))
	}
	if vars := explanation.FailedVars(); vars != nil {
		t.Fatalf("expected no failing vars, got %v", vars)
	}
}

/*
	Only conjunctions are broken apart. Anything else fails as a whole, because no single part of it
	is the cause on its own.
*/
func TestExplainReportsNonConjunctionsWhole(t *testing.T) {

	cases := []struct {
		expression string
		parameters map[string]interface{}
		expected   string
	}{
		{
			expression: "!(company in ('State Farm', 'Geico'))",
			parameters: map[string]interface{}{"company": "Geico"},
			expected:   `!(company in [...2 values])`,
		},
		{
			expression: "a > 1 || b > 2",
			parameters: map[string]interface{}{"a": 0, "b": 0},
			expected:   "a > 1 || b > 2",
		},
		{
			expression: "(a > 1 || b > 2) && c > 3",
			parameters: map[string]interface{}{"a": 0, "b": 0, "c": 5},
			expected:   "a > 1 || b > 2",
		},
	}

	for _, testCase := range cases {

		expression := mustExpression(t, testCase.expression)

		_, explanation, err := expression.EvaluateWithExplanation(testCase.parameters, ExplainAll)
		if err != nil {
			t.Fatalf("[%s] %v", testCase.expression, err)
		}

		texts := failedTermTexts(explanation)
		if len(texts) != 1 || texts[0] != testCase.expected {
			t.Errorf("[%s] expected %q, got %v", testCase.expression, testCase.expected, texts)
		}
	}
}

/*
	`in` lists are condensed into a set at parse time and are routinely hundreds of entries long, so
	they are summarized rather than printed.
*/
func TestExplainSummarizesLargeLists(t *testing.T) {

	values := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		values = append(values, fmt.Sprintf("'%d'", i))
	}

	expression := mustExpression(t, "sub_1 in ("+strings.Join(values, ", ")+")")

	_, explanation, err := expression.EvaluateWithExplanation(map[string]interface{}{"sub_1": "nope"}, ExplainAll)
	if err != nil {
		t.Fatal(err)
	}

	texts := failedTermTexts(explanation)
	if len(texts) != 1 || texts[0] != "sub_1 in [...20 values]" {
		t.Fatalf("expected the list to be summarized, got %v", texts)
	}
}

/*
	A failing function call is reported by name, with the parameters its arguments read.
*/
func TestExplainReportsFunctionCalls(t *testing.T) {

	functions := map[string]ExpressionFunction{
		"all": func(arguments ...interface{}) (interface{}, error) {
			return false, nil
		},
	}

	expression, err := NewEvaluableExpressionWithFunctions("age > 18 && all(drivers, 'clean')", functions)
	if err != nil {
		t.Fatal(err)
	}

	parameters := map[string]interface{}{"age": 30, "drivers": []interface{}{"a"}}

	_, explanation, err := expression.EvaluateWithExplanation(parameters, ExplainAll)
	if err != nil {
		t.Fatal(err)
	}

	terms := explanation.FailedTerms()
	if len(terms) != 1 {
		t.Fatalf("expected one failing term, got %v", failedTermTexts(explanation))
	}
	if terms[0].Function != "all" {
		t.Errorf("expected the function name to be recorded, got %q", terms[0].Function)
	}
	if terms[0].Text != `all(drivers, "clean")` {
		t.Errorf("unexpected rendering: %q", terms[0].Text)
	}
	if terms[0].Vars["drivers"] == nil {
		t.Errorf("expected the collection parameter to be recorded, got %v", terms[0].Vars)
	}
}

/*
	A branch that only ran for the explanation cannot change the outcome, and an error it raises is
	recorded on its node instead of failing the evaluation.
*/
func TestExplainAllContainsErrorsFromSkippedBranches(t *testing.T) {

	expression := mustExpression(t, "a > 1 && b > 2")
	parameters := map[string]interface{}{"a": 0} // b is missing, so `b > 2` cannot be evaluated

	result, explanation, err := expression.EvaluateWithExplanation(parameters, ExplainAll)
	if err != nil {
		t.Fatalf("the error of a skipped branch must not be returned, got %v", err)
	}
	if result != false {
		t.Fatalf("expected false, got %v", result)
	}

	terms := explanation.FailedTerms()
	if len(terms) != 2 {
		t.Fatalf("expected both terms to be reported, got %v", failedTermTexts(explanation))
	}
	if terms[1].Err == nil {
		t.Errorf("expected the error of the skipped branch to be recorded on its node")
	}
	if !terms[1].Skipped {
		t.Errorf("expected the branch to be marked as skipped")
	}
}

/*
	An error on the live path is still returned, in either mode, exactly as Eval would.
*/
func TestExplainReturnsErrorsFromTheLivePath(t *testing.T) {

	expression := mustExpression(t, "a > 1 && b > 2")
	parameters := map[string]interface{}{"b": 5} // a is missing, and a is evaluated first

	for _, mode := range []ExplainMode{ExplainFirst, ExplainAll} {

		_, explanation, err := expression.EvaluateWithExplanation(parameters, mode)
		if err == nil {
			t.Fatalf("[mode %d] expected an error", mode)
		}
		if explanation.Root == nil {
			t.Errorf("[mode %d] expected an explanation even alongside an error", mode)
		}
	}
}

/*
	Explaining must never change what an expression evaluates to.
*/
func TestExplainMatchesEval(t *testing.T) {

	cases := []struct {
		expression string
		parameters map[string]interface{}
	}{
		{"a > 1 && b > 2", map[string]interface{}{"a": 5, "b": 5}},
		{"a > 1 && b > 2", map[string]interface{}{"a": 0, "b": 5}},
		{"a > 1 || b > 2", map[string]interface{}{"a": 5, "b": 0}},
		{"a > 1 ? 'yes' : 'no'", map[string]interface{}{"a": 5}},
		{"a > 1 ? 'yes' : 'no'", map[string]interface{}{"a": 0}},
		{"a ?? 'fallback'", map[string]interface{}{"a": nil}},
		{"a ?? 'fallback'", map[string]interface{}{"a": "set"}},
		{"(a + b) * c > 10", map[string]interface{}{"a": 1, "b": 2, "c": 4}},
		{"name =~ '^d'", map[string]interface{}{"name": "daniel"}},
		{"a in (1, 2, 3)", map[string]interface{}{"a": 2}},
	}

	for _, testCase := range cases {

		expression := mustExpression(t, testCase.expression)

		expected, expectedErr := expression.Evaluate(testCase.parameters)

		for _, mode := range []ExplainMode{ExplainFirst, ExplainAll} {

			actual, _, actualErr := expression.EvaluateWithExplanation(testCase.parameters, mode)

			if (expectedErr == nil) != (actualErr == nil) {
				t.Errorf("[%s mode %d] error mismatch: %v vs %v", testCase.expression, mode, expectedErr, actualErr)
				continue
			}
			if actual != expected {
				t.Errorf("[%s mode %d] expected %v, got %v", testCase.expression, mode, expected, actual)
			}
		}
	}
}

/*
	Stage reordering swaps the contents of stages around, and the rendered text has to travel with them.
*/
func TestExplainRendersReorderedStages(t *testing.T) {

	expression := mustExpression(t, "10 - a - b > 0")

	_, explanation, err := expression.EvaluateWithExplanation(map[string]interface{}{"a": 20, "b": 5}, ExplainAll)
	if err != nil {
		t.Fatal(err)
	}

	texts := failedTermTexts(explanation)
	if len(texts) != 1 || texts[0] != "10 - a - b > 0" {
		t.Fatalf("unexpected rendering: %v", texts)
	}
}
