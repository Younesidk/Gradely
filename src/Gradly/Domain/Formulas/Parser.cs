namespace Gradly.Domain.Formulas;

/// <summary>
/// Recursive-descent parser.
///   expr    := term (('+'|'-') term)*
///   term    := unary (('*'|'/') unary)*
///   unary   := ('-'|'+') unary | primary
///   primary := NUMBER | IDENT | IDENT '(' [expr (',' expr)*] ')' | '(' expr ')'
/// Nesting depth is capped so hostile input cannot overflow the stack.
/// </summary>
internal sealed class Parser
{
    public const int MaxDepth = 48;

    private readonly List<Token> _tokens;
    private int _pos;
    private int _depth;

    private Parser(List<Token> tokens) => _tokens = tokens;

    public static bool TryParse(string text, out Node? root, out FormulaError? error)
    {
        root = null;
        if (!Lexer.TryTokenize(text, out var tokens, out error)) return false;
        var parser = new Parser(tokens);
        try
        {
            root = parser.ParseExpression();
            if (parser.Current.Kind != TokenKind.End)
                throw new ParseException(parser.Current.Kind == TokenKind.RParen
                    ? "Unmatched ')'."
                    : $"Unexpected '{parser.Current.Text}'. Did you forget an operator?", parser.Current.Position);
            return true;
        }
        catch (ParseException ex)
        {
            root = null;
            error = new FormulaError(ex.Message, ex.Position);
            return false;
        }
    }

    private sealed class ParseException(string message, int position) : Exception(message)
    {
        public int Position { get; } = position;
    }

    private Token Current => _tokens[_pos];
    private Token Advance() => _tokens[_pos++];

    private Node ParseExpression()
    {
        Enter();
        var left = ParseTerm();
        while (Current.Kind is TokenKind.Plus or TokenKind.Minus)
        {
            var op = Advance();
            var right = ParseTerm();
            left = new BinaryNode(op.Text[0], left, right, op.Position);
        }
        Leave();
        return left;
    }

    private Node ParseTerm()
    {
        var left = ParseUnary();
        while (Current.Kind is TokenKind.Star or TokenKind.Slash)
        {
            var op = Advance();
            var right = ParseUnary();
            left = new BinaryNode(op.Text[0], left, right, op.Position);
        }
        return left;
    }

    private Node ParseUnary()
    {
        if (Current.Kind is TokenKind.Plus or TokenKind.Minus)
        {
            var op = Advance();
            Enter();
            var operand = ParseUnary();
            Leave();
            return new UnaryNode(op.Text[0], operand, op.Position);
        }
        return ParsePrimary();
    }

    private Node ParsePrimary()
    {
        var t = Current;
        switch (t.Kind)
        {
            case TokenKind.Number:
                Advance();
                return new NumberNode(t.Number, t.Position);

            case TokenKind.Identifier:
                Advance();
                if (Current.Kind == TokenKind.LParen)
                {
                    Advance();
                    var args = new List<Node>();
                    if (Current.Kind != TokenKind.RParen)
                    {
                        args.Add(ParseExpression());
                        while (Current.Kind == TokenKind.Comma)
                        {
                            Advance();
                            args.Add(ParseExpression());
                        }
                    }
                    if (Current.Kind != TokenKind.RParen)
                        throw new ParseException($"Expected ')' to close {t.Text}(.", Current.Position);
                    Advance();
                    return new CallNode(t.Text, args, t.Position);
                }
                return new VariableNode(t.Text, t.Position);

            case TokenKind.LParen:
                Advance();
                var inner = ParseExpression();
                if (Current.Kind != TokenKind.RParen)
                    throw new ParseException("Missing closing ')'.", Current.Position);
                Advance();
                return inner;

            case TokenKind.End:
                throw new ParseException("The expression ended unexpectedly.", t.Position);

            default:
                throw new ParseException($"Unexpected '{t.Text}'.", t.Position);
        }
    }

    private void Enter()
    {
        if (++_depth > MaxDepth)
            throw new ParseException("The expression is nested too deeply.", Current.Position);
    }

    private void Leave() => _depth--;
}
