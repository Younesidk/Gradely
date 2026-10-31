namespace Gradly.Domain.Formulas;

/// <summary>Result of <see cref="Formula.Parse"/>.</summary>
public sealed class FormulaParseResult
{
    internal FormulaParseResult(Formula? formula, IReadOnlyList<FormulaError> errors)
    {
        Formula = formula;
        Errors = errors;
    }

    public Formula? Formula { get; }
    public IReadOnlyList<FormulaError> Errors { get; }
    public bool Success => Formula is not null && Errors.Count == 0;
    public string? FirstError => Errors.Count > 0 ? Errors[0].Message : null;
}

/// <summary>A value of the form A + B·x where x is the single unknown variable (if any).</summary>
internal readonly record struct Linear(double A, double B)
{
    public bool DependsOnUnknown => B != 0;
}

/// <summary>
/// A parsed, validated grading formula. Formulas are data: they are parsed into an AST and
/// interpreted by this class only. No code is ever compiled or executed.
/// </summary>
public sealed class Formula
{
    public const int MaxLength = 500;

    private readonly Node _root;

    private Formula(string text, Node root, IReadOnlySet<string> variables)
    {
        Text = text;
        _root = root;
        Variables = variables;
    }

    public string Text { get; }

    /// <summary>Canonical names of the variables referenced by the formula.</summary>
    public IReadOnlySet<string> Variables { get; }

    /// <summary>Parses text and checks variables / functions. Never throws for bad user input.</summary>
    /// <param name="allowLab">Whether the Lab variable is available (module has a lab exam).</param>
    public static FormulaParseResult Parse(string? text, bool allowLab = true)
    {
        if (string.IsNullOrWhiteSpace(text))
            return Fail("Enter a formula, for example (CC1 + CC2) / 2 * 0.4 + Exam * 0.6.");
        text = text.Trim();
        if (text.Length > MaxLength)
            return Fail($"The formula is too long (maximum {MaxLength} characters).");

        if (!Parser.TryParse(text, out var root, out var error))
            return Fail(error!.Message, error.Position);

        var errors = new List<FormulaError>();
        var variables = new HashSet<string>(StringComparer.Ordinal);
        Validate(root!, allowLab, variables, errors);
        if (errors.Count > 0) return new FormulaParseResult(null, errors);

        return new FormulaParseResult(new Formula(text, root!, variables), []);

        static FormulaParseResult Fail(string message, int position = -1) =>
            new(null, [new FormulaError(message, position)]);
    }

    private static void Validate(Node node, bool allowLab, HashSet<string> variables, List<FormulaError> errors)
    {
        switch (node)
        {
            case VariableNode v:
                var canonical = FormulaVariables.Canonicalize(v.Name);
                if (canonical is null)
                    errors.Add(new FormulaError($"Unknown variable '{v.Name}'. Use CC1, CC2, Lab or Exam.", v.Position));
                else if (canonical == FormulaVariables.Lab && !allowLab)
                    errors.Add(new FormulaError("Lab is not available: this formula is for modules without a lab exam.", v.Position));
                else
                    variables.Add(canonical);
                break;
            case UnaryNode u:
                Validate(u.Operand, allowLab, variables, errors);
                break;
            case BinaryNode b:
                Validate(b.Left, allowLab, variables, errors);
                Validate(b.Right, allowLab, variables, errors);
                break;
            case CallNode c:
                var sig = FormulaFunctions.Find(c.Name);
                if (sig is null)
                    errors.Add(new FormulaError($"Unknown function '{c.Name}'. Available: AVG, MIN, MAX, ABS, ROUND.", c.Position));
                else if (c.Args.Count < sig.MinArgs || c.Args.Count > sig.MaxArgs)
                    errors.Add(new FormulaError(sig.MinArgs == sig.MaxArgs
                        ? $"{sig.Name}() takes exactly {sig.MinArgs} argument(s)."
                        : $"{sig.Name}() takes {sig.MinArgs}{(sig.MaxArgs == int.MaxValue ? " or more" : $" to {sig.MaxArgs}")} arguments.", c.Position));
                foreach (var a in c.Args) Validate(a, allowLab, variables, errors);
                break;
        }
    }

    /// <summary>Evaluates the formula. Throws <see cref="FormulaEvaluationException"/> on missing values or invalid math.</summary>
    public double Evaluate(IReadOnlyDictionary<string, double> values)
    {
        var result = Eval(_root, values, unknown: null);
        return Finite(result.A);
    }

    public bool TryEvaluate(IReadOnlyDictionary<string, double> values, out double result, out string? error)
    {
        try
        {
            result = Evaluate(values);
            error = null;
            return true;
        }
        catch (FormulaEvaluationException ex)
        {
            result = 0;
            error = ex.Message;
            return false;
        }
    }

    /// <summary>
    /// Expresses the formula as A + B·unknown, with every other variable taken from <paramref name="values"/>.
    /// Throws <see cref="FormulaNotLinearException"/> if the formula is not provably linear in the unknown
    /// (e.g. MAX(Exam, 10), Exam * Exam, 1 / Exam) so callers never fabricate an answer.
    /// </summary>
    public (double A, double B) Linearize(string unknown, IReadOnlyDictionary<string, double> values)
    {
        var lin = Eval(_root, values, unknown);
        return (Finite(lin.A), Finite(lin.B));
    }

    private static double Finite(double d) =>
        double.IsFinite(d) ? d : throw new FormulaEvaluationException("The result is not a finite number.");

    private static Linear Eval(Node node, IReadOnlyDictionary<string, double> values, string? unknown)
    {
        switch (node)
        {
            case NumberNode n:
                return new Linear(n.Value, 0);

            case VariableNode v:
            {
                var name = FormulaVariables.Canonicalize(v.Name)
                           ?? throw new FormulaEvaluationException($"Unknown variable '{v.Name}'.");
                if (unknown is not null && name == unknown) return new Linear(0, 1);
                if (!values.TryGetValue(name, out var val))
                    throw new FormulaEvaluationException($"No value for {name}.");
                return new Linear(val, 0);
            }

            case UnaryNode u:
            {
                var o = Eval(u.Operand, values, unknown);
                return u.Op == '-' ? new Linear(-o.A, -o.B) : o;
            }

            case BinaryNode b:
            {
                var l = Eval(b.Left, values, unknown);
                var r = Eval(b.Right, values, unknown);
                switch (b.Op)
                {
                    case '+': return new Linear(l.A + r.A, l.B + r.B);
                    case '-': return new Linear(l.A - r.A, l.B - r.B);
                    case '*':
                        if (l.DependsOnUnknown && r.DependsOnUnknown)
                            throw new FormulaNotLinearException("The unknown variable is multiplied by itself.");
                        return l.DependsOnUnknown
                            ? new Linear(l.A * r.A, l.B * r.A)
                            : new Linear(l.A * r.A, l.A * r.B);
                    case '/':
                        if (r.DependsOnUnknown)
                            throw new FormulaNotLinearException("The unknown variable appears in a denominator.");
                        if (r.A == 0)
                            throw new FormulaEvaluationException("Division by zero.");
                        return new Linear(l.A / r.A, l.B / r.A);
                }
                throw new FormulaEvaluationException($"Unsupported operator '{b.Op}'.");
            }

            case CallNode c:
            {
                var args = new Linear[c.Args.Count];
                for (var i = 0; i < args.Length; i++) args[i] = Eval(c.Args[i], values, unknown);
                var name = c.Name.ToUpperInvariant();

                if (name == "AVG")
                {
                    var a = 0.0; var bsum = 0.0;
                    foreach (var x in args) { a += x.A; bsum += x.B; }
                    return new Linear(a / args.Length, bsum / args.Length);
                }

                if (args.Any(x => x.DependsOnUnknown))
                    throw new FormulaNotLinearException($"{name}() is applied to the unknown variable.");

                return name switch
                {
                    "MIN" => new Linear(args.Min(x => x.A), 0),
                    "MAX" => new Linear(args.Max(x => x.A), 0),
                    "ABS" => new Linear(Math.Abs(args[0].A), 0),
                    "ROUND" => new Linear(RoundTo(args[0].A, args.Length > 1 ? args[1].A : 0), 0),
                    _ => throw new FormulaEvaluationException($"Unknown function '{c.Name}'."),
                };
            }
        }
        throw new FormulaEvaluationException("Unsupported expression.");
    }

    private static double RoundTo(double value, double digits)
    {
        var d = (int)Math.Clamp(Math.Round(digits), 0, 10);
        return Math.Round(value, d, MidpointRounding.AwayFromZero);
    }

    public override string ToString() => Text;
}
