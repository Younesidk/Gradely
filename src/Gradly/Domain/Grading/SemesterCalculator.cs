namespace Gradly.Domain.Grading;

/// <summary>Weighted semester average. Incomplete modules are excluded, never counted as zero.</summary>
public static class SemesterCalculator
{
    public const double DefaultScale = 20;

    public static SemesterSummary Summarize(IReadOnlyCollection<ModuleScore> modules)
    {
        if (modules.Count == 0) return SemesterSummary.Empty;

        var complete = modules.Where(m => m.Result.IsComplete && m.Result.Grade is not null).ToList();
        var passed = complete.Count(m => m.Result.Passed == true);
        var failed = complete.Count - passed;

        // Modules may use different maximum grades; express everything on the most common scale.
        var scale = complete.Count == 0
            ? DefaultScale
            : complete.GroupBy(m => m.MaxGrade)
                .OrderByDescending(g => g.Count()).ThenByDescending(g => g.Key)
                .First().Key;

        var totalCoefficient = modules.Sum(m => m.Coefficient);
        var completedCoefficient = complete.Sum(m => m.Coefficient);

        double? average = null;
        if (completedCoefficient > 0)
        {
            var weighted = complete.Sum(m => m.Result.Grade!.Value / m.MaxGrade * scale * m.Coefficient);
            average = GradeCalculator.Round(weighted / completedCoefficient);
        }

        return new SemesterSummary(average, scale, modules.Count, complete.Count, modules.Count - complete.Count,
            passed, failed, totalCoefficient, completedCoefficient);
    }
}
