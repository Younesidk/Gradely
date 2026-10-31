using System.Globalization;

namespace Gradly.Domain.Grading;

/// <summary>Where a module stands in its calculation.</summary>
public enum ModuleStatus
{
    /// <summary>No formula has been selected for the module.</summary>
    NoFormula,
    /// <summary>The selected formula is invalid for this module or cannot be evaluated.</summary>
    FormulaError,
    /// <summary>No grade has been entered yet.</summary>
    WaitingForGrades,
    /// <summary>Some grades the formula needs are still missing.</summary>
    Incomplete,
    /// <summary>All grades the formula needs are present; a final grade is available.</summary>
    Complete,
}

public enum RequiredExamKind
{
    /// <summary>Exam grade is irrelevant or already entered.</summary>
    NotApplicable,
    /// <summary>A minimum exam grade was computed.</summary>
    Needed,
    /// <summary>The passing threshold is already reached whatever the exam grade is.</summary>
    AlreadySecured,
    /// <summary>Even the maximum exam grade cannot reach the passing threshold.</summary>
    Impossible,
    /// <summary>Other grades are missing, so the exam requirement cannot be computed yet.</summary>
    MoreGradesRequired,
    /// <summary>The formula cannot be reliably solved for the exam grade.</summary>
    Unsupported,
}

/// <summary>Everything the calculation engine needs to know about a module.</summary>
public sealed record ModuleInput(
    double? Cc1,
    double? Cc2,
    double? Lab,
    double? Exam,
    bool HasLabExam,
    double MaxGrade,
    double PassingThreshold);

public sealed record RequiredExam(RequiredExamKind Kind, double? Value, string Message)
{
    public static readonly RequiredExam None = new(RequiredExamKind.NotApplicable, null, "");
}

public sealed record ModuleResult(
    ModuleStatus Status,
    double? Grade,
    bool? Passed,
    string StatusText,
    IReadOnlyList<string> MissingInputs,
    RequiredExam RequiredExam,
    string? FormulaError)
{
    public bool IsComplete => Status == ModuleStatus.Complete;
}

public sealed record ModuleScore(double Coefficient, double MaxGrade, ModuleResult Result);

public sealed record SemesterSummary(
    double? Average,
    double Scale,
    int ModuleCount,
    int CompletedCount,
    int IncompleteCount,
    int PassedCount,
    int FailedCount,
    double TotalCoefficient,
    double CompletedCoefficient)
{
    /// <summary>True when the average does not yet cover every module.</summary>
    public bool IsProvisional => IncompleteCount > 0;

    public static readonly SemesterSummary Empty = new(null, 20, 0, 0, 0, 0, 0, 0, 0);
}

public static class GradeFormat
{
    /// <summary>Formats a grade with up to two decimals, no trailing zeros, culture-invariant.</summary>
    public static string Number(double? value) =>
        value is null ? "—" : value.Value.ToString("0.##", CultureInfo.InvariantCulture);

    /// <summary>Always two decimals ("12.50").</summary>
    public static string Fixed2(double? value) =>
        value is null ? "—" : value.Value.ToString("0.00", CultureInfo.InvariantCulture);
}
