using Gradly.Domain.Grading;

namespace Gradly.Tests.Engine;

public class SemesterCalculatorTests
{
    private const string NoLab = GradeCalculator.DefaultNoLabFormula;

    private static ModuleScore Score(double coeff, double? cc1, double? cc2, double? exam, double max = 20, double pass = 10) =>
        new(coeff, max, GradeCalculator.Calculate(new ModuleInput(cc1, cc2, null, exam, false, max, pass), NoLab));

    [Fact]
    public void Empty_semester()
    {
        var s = SemesterCalculator.Summarize([]);
        Assert.Null(s.Average);
        Assert.Equal(0, s.ModuleCount);
        Assert.False(s.IsProvisional);
    }

    [Fact]
    public void Weighted_average_with_different_coefficients()
    {
        var a = Score(3, 12, 14, 13);  // 13
        var b = Score(1, 8, 8, 8);     // 8
        var s = SemesterCalculator.Summarize([a, b]);
        Assert.Equal((13 * 3 + 8 * 1) / 4.0, s.Average!.Value, 10); // 11.75
        Assert.Equal(2, s.CompletedCount);
        Assert.Equal(1, s.PassedCount);
        Assert.Equal(1, s.FailedCount);
        Assert.False(s.IsProvisional);
    }

    [Fact]
    public void Equal_coefficients_is_the_plain_mean()
    {
        var s = SemesterCalculator.Summarize([Score(1, 10, 10, 10), Score(1, 16, 16, 16)]);
        Assert.Equal(13, s.Average);
    }

    [Fact]
    public void Incomplete_modules_are_excluded_and_make_the_average_provisional()
    {
        var done = Score(2, 12, 14, 13);
        var pending = Score(5, 10, 10, null);
        var empty = Score(5, null, null, null);
        var s = SemesterCalculator.Summarize([done, pending, empty]);
        Assert.Equal(13, s.Average); // not dragged down by the heavy incomplete modules
        Assert.Equal(3, s.ModuleCount);
        Assert.Equal(1, s.CompletedCount);
        Assert.Equal(2, s.IncompleteCount);
        Assert.True(s.IsProvisional);
        Assert.Equal(12, s.TotalCoefficient);
        Assert.Equal(2, s.CompletedCoefficient);
    }

    [Fact]
    public void No_completed_modules_gives_no_average()
    {
        var s = SemesterCalculator.Summarize([Score(1, null, null, null), Score(1, 10, null, null)]);
        Assert.Null(s.Average);
        Assert.Equal(2, s.IncompleteCount);
        Assert.True(s.IsProvisional);
    }

    [Fact]
    public void Zero_grade_counts_in_the_average()
    {
        var s = SemesterCalculator.Summarize([Score(1, 0, 0, 0), Score(1, 20, 20, 20)]);
        Assert.Equal(10, s.Average);
        Assert.Equal(1, s.FailedCount);
    }

    [Fact]
    public void Zero_total_coefficient_gives_no_average()
    {
        var s = SemesterCalculator.Summarize([Score(0, 12, 14, 13)]);
        Assert.Null(s.Average);
        Assert.Equal(1, s.CompletedCount);
    }

    [Fact]
    public void Mixed_scales_are_normalised_to_the_most_common_scale()
    {
        var a = Score(1, 12, 14, 13);                       // 13/20
        var b = Score(1, 10, 10, 10);                       // 10/20
        var c = Score(1, 50, 50, 50, max: 100, pass: 50);   // 50/100 -> 10/20
        var s = SemesterCalculator.Summarize([a, b, c]);
        Assert.Equal(20, s.Scale);
        Assert.Equal(Math.Round((13 + 10 + 10) / 3.0, 2), s.Average);
    }

    [Fact]
    public void Average_is_rounded_to_two_decimals()
    {
        var s = SemesterCalculator.Summarize([Score(1, 10, 10, 10), Score(1, 10, 10, 11), Score(1, 10, 10, 11)]);
        Assert.Equal(Math.Round((10 + 10.6 + 10.6) / 3, 2, MidpointRounding.AwayFromZero), s.Average);
    }
}
