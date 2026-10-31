namespace Gradly.Domain.Formulas;

/// <summary>A syntax / semantic problem found while parsing or validating a formula.</summary>
public sealed record FormulaError(string Message, int Position = -1);

/// <summary>Thrown when a parsed formula cannot be evaluated for the supplied values.</summary>
public sealed class FormulaEvaluationException(string message) : Exception(message);

/// <summary>Thrown by the linearizer when the formula is not a linear function of the unknown variable.</summary>
public sealed class FormulaNotLinearException(string message) : Exception(message);

/// <summary>The variables a grading formula may reference.</summary>
public static class FormulaVariables
{
    public const string Cc1 = "CC1";
    public const string Cc2 = "CC2";
    public const string Lab = "Lab";
    public const string Exam = "Exam";

    public static readonly IReadOnlyList<string> All = [Cc1, Cc2, Lab, Exam];
    public static readonly IReadOnlyList<string> WithoutLab = [Cc1, Cc2, Exam];

    /// <summary>Returns the canonical (correctly cased) variable name, or null when unknown.</summary>
    public static string? Canonicalize(string name) =>
        All.FirstOrDefault(v => string.Equals(v, name, StringComparison.OrdinalIgnoreCase));

    public static IReadOnlyList<string> For(bool hasLabExam) => hasLabExam ? All : WithoutLab;
}

/// <summary>The functions a grading formula may call.</summary>
public static class FormulaFunctions
{
    public sealed record Signature(string Name, int MinArgs, int MaxArgs, string Description);

    public static readonly IReadOnlyList<Signature> All =
    [
        new("AVG", 1, int.MaxValue, "Average of its arguments"),
        new("MIN", 1, int.MaxValue, "Smallest argument"),
        new("MAX", 1, int.MaxValue, "Largest argument"),
        new("ABS", 1, 1, "Absolute value"),
        new("ROUND", 1, 2, "Round to n decimals (default 0)"),
    ];

    public static Signature? Find(string name) =>
        All.FirstOrDefault(f => string.Equals(f.Name, name, StringComparison.OrdinalIgnoreCase));
}
