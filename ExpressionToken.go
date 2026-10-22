package govaluate

/*
	Represents a single parsed token.
*/
type ExpressionToken struct {
	Kind  TokenKind
	Value interface{}

	/*
		The source text of this token, for tokens whose Value does not carry it.
		Currently only set for FUNCTION tokens, whose Value is the function itself.
	*/
	Name string
}
