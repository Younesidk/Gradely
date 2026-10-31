using Gradly.Domain.Formulas;

namespace Gradly.Domain.Grading;

/// <summary>
/// Pure grade calculation: no UI, database or framework dependencies.
/// A missing grade (null) is never treated as zero; an entered 0 is a real grade.
/// </summary>
public static class GradeCalculator
{
    public const int Decimals = 2;
    private const double Epsilon = 1e-9;

    /// <summary>Default formula for modules without a lab exam.</summary>
    public const string DefaultNoLabFormula = "(CC1 + CC2) / 2 * 0.4 + Exam * 0.6";

    public static double Round(double value) => Math.Round(value, Decimals, MidpointRounding.AwayFromZero);

    public static ModuleResult Calculate(ModuleInput module, string? formulaExpression)
    {
        if (string.IsNullOrWhiteSpace(formulaExpression))
            return Result(ModuleStatus.NoFormula, "No formula selected");

        var parsed = Formula.Parse(formulaExpression, allowLab: module.HasLabExam);
        if (!parsed.Success)
            return Result(ModuleStatus.FormulaError, "Formula error", error: parsed.FirstError);
        var formula = parsed.Formula!;

        var values = CollectValues(module);
        var missing = FormulaVariables.For(module.HasLabExam)
            .Where(v => formula.Variables.Contains(v) && !values.ContainsKey(v))
            .ToList();

        if (values.Count == 0 && formula.Variables.Count > 0)
            return Result(ModuleStatus.WaitingForGrades, "Waiting for grades", missing: missing);

        if (missing.Count == 0)
        {
            if (!formula.TryEvaluate(values, out var raw, out var evalError))
                return Result(ModuleStatus.FormulaError, "Formula error", error: evalError);
            var grade = Round(raw);
            var passed = grade + Epsilon >= module.PassingThreshold;
            return new ModuleResult(ModuleStatus.Complete, grade, passed, passed ? "Passed" : "Failed",
                missing, RequiredExam.None, null);
        }

        var required = RequiredExam.None;
        if (missing.Contains(FormulaVariables.Exam))
        {
            required = missing.Count == 1
                ? SolveRequiredExam(formula, values, module)
                : new RequiredExam(RequiredExamKind.MoreGradesRequired, null,
                    $"Enter {Join(missing.Where(m => m != FormulaVariables.Exam))} to calculate the exam grade you need.");
        }

        return new ModuleResult(ModuleStatus.Incomplete, null, null,
            $"Incomplete — missing {Join(missing)}", missing, required, null);
    }

    /// <summary>
    /// Smallest exam grade (rounded up to 2 decimals) for which the formula reaches the passing threshold.
    /// Only answers when the formula is provably linear and increasing in Exam.
    /// </summary>
    public static RequiredExam SolveRequiredExam(Formula formula, IReadOnlyDictionary<string, double> values, ModuleInput module)
    {
        var unsupported = new RequiredExam(RequiredExamKind.Unsupported, null,
            "This formula's structure doesn't allow a reliable required-exam calculation.");

        double a, b;
        try
        {
            (a, b) = formula.Linearize(FormulaVariables.Exam, values);
        }
        catch (FormulaNotLinearException)
        {
            return unsupported;
        }
        catch (FormulaEvaluationException)
        {
            return unsupported;
        }

        if (b <= 1e-12) return unsupported; // exam has no effect (or lowers the grade): nothing sensible to solve

        var required = (module.PassingThreshold - a) / b;
        if (required <= Epsilon)
            return new RequiredExam(RequiredExamKind.AlreadySecured, 0,
                $"You've already secured the passing threshold ({GradeFormat.Number(module.PassingThreshold)}/{GradeFormat.Number(module.MaxGrade)}) — any exam grade will do.");

        if (required > module.MaxGrade + Epsilon)
            return new RequiredExam(RequiredExamKind.Impossible, Math.Round(required, Decimals),
                $"Passing is mathematically impossible under this formula: you would need {GradeFormat.Number(Math.Round(required, Decimals))}/{GradeFormat.Number(module.MaxGrade)} in the exam.");

        var ceiled = Math.Ceiling(required * 100 - 1e-6) / 100;
        return new RequiredExam(RequiredExamKind.Needed, ceiled,
            $"You need at least {GradeFormat.Number(ceiled)}/{GradeFormat.Number(module.MaxGrade)} in your final exam to pass.");
    }

    private static Dictionary<string, double> CollectValues(ModuleInput m)
    {
        var d = new Dictionary<string, double>(4, StringComparer.Ordinal);
        if (m.Cc1 is { } c1) d[FormulaVariables.Cc1] = c1;
        if (m.Cc2 is { } c2) d[FormulaVariables.Cc2] = c2;
        if (m.HasLabExam && m.Lab is { } lab) d[FormulaVariables.Lab] = lab;
        if (m.Exam is { } ex) d[FormulaVariables.Exam] = ex;
        return d;
    }

    private static string Join(IEnumerable<string> names) => string.Join(", ", names);

    private static ModuleResult Result(ModuleStatus status, string text, string? error = null, List<string>? missing = null) =>
        new(status, null, null, text, missing ?? [], RequiredExam.None, error);
}
