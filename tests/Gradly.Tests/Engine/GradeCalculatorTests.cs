using Gradly.Domain.Grading;

namespace Gradly.Tests.Engine;

public class GradeCalculatorTests
{
    private const string NoLab = GradeCalculator.DefaultNoLabFormula;
    private const string LabFormula = "(CC1 + CC2) / 2 * 0.2 + Lab * 0.2 + Exam * 0.6"; // test-only weighting

    private static ModuleInput M(double? cc1 = null, double? cc2 = null, double? lab = null, double? exam = null,
        bool hasLab = false, double max = 20, double pass = 10) => new(cc1, cc2, lab, exam, hasLab, max, pass);

    [Fact]
    public void No_grades_waits()
    {
        var r = GradeCalculator.Calculate(M(), NoLab);
        Assert.Equal(ModuleStatus.WaitingForGrades, r.Status);
        Assert.Equal("Waiting for grades", r.StatusText);
        Assert.Null(r.Grade);
        Assert.Null(r.Passed);
    }

    [Fact]
    public void Default_formula_full_grades()
    {
        var r = GradeCalculator.Calculate(M(12, 14, exam: 13), NoLab);
        Assert.Equal(ModuleStatus.Complete, r.Status);
        Assert.Equal(13 * 0.4 + 13 * 0.6, r.Grade!.Value, 10);
        Assert.True(r.Passed);
        Assert.Equal("Passed", r.StatusText);
    }

    [Fact]
    public void Required_exam_for_spec_example_is_8()
    {
        var r = GradeCalculator.Calculate(M(12, 14), NoLab);
        Assert.Equal(ModuleStatus.Incomplete, r.Status);
        Assert.Equal(RequiredExamKind.Needed, r.RequiredExam.Kind);
        Assert.Equal(8, r.RequiredExam.Value);
        Assert.Equal("You need at least 8/20 in your final exam to pass.", r.RequiredExam.Message);
        Assert.Null(r.Grade);
        Assert.Equal(["Exam"], r.MissingInputs);
    }

    [Fact]
    public void Required_exam_is_rounded_up_to_two_decimals()
    {
        // 0.4*avg(10,11)=4.2 ; (10-4.2)/0.6 = 9.6666…
        var r = GradeCalculator.Calculate(M(10, 11), NoLab);
        Assert.Equal(9.67, r.RequiredExam.Value);
        // Using exactly that exam grade must pass.
        var check = GradeCalculator.Calculate(M(10, 11, exam: 9.67), NoLab);
        Assert.True(check.Passed);
        var below = GradeCalculator.Calculate(M(10, 11, exam: 9.65), NoLab); // 9.99
        Assert.False(below.Passed);
    }

    [Fact]
    public void Already_secured_when_required_is_negative()
    {
        // CC are 20/20 -> 8 points; need 2 more -> 3.33. Use a formula with higher CC weight instead.
        var r = GradeCalculator.Calculate(M(20, 20), "(CC1 + CC2) / 2 * 0.8 + Exam * 0.2");
        Assert.Equal(RequiredExamKind.AlreadySecured, r.RequiredExam.Kind);
        Assert.Contains("already secured", r.RequiredExam.Message);
        Assert.Equal(ModuleStatus.Incomplete, r.Status);
    }

    [Fact]
    public void Impossible_when_required_exceeds_max()
    {
        var r = GradeCalculator.Calculate(M(0, 0), NoLab); // 0 + 0.6*20 = 12 -> possible
        Assert.Equal(RequiredExamKind.Needed, r.RequiredExam.Kind);
        Assert.Equal(16.67, r.RequiredExam.Value);

        var impossible = GradeCalculator.Calculate(M(0, 0), "(CC1 + CC2) / 2 * 0.8 + Exam * 0.2"); // need 50
        Assert.Equal(RequiredExamKind.Impossible, impossible.RequiredExam.Kind);
        Assert.Contains("impossible", impossible.RequiredExam.Message);
    }

    [Fact]
    public void Exactly_max_exam_needed_is_possible()
    {
        // need 20 exactly: 0.5*avg + 0.5*exam, avg=0 -> exam 20
        var r = GradeCalculator.Calculate(M(0, 0), "(CC1 + CC2) / 2 * 0.5 + Exam * 0.5");
        Assert.Equal(RequiredExamKind.Needed, r.RequiredExam.Kind);
        Assert.Equal(20, r.RequiredExam.Value);
    }

    [Fact]
    public void Actual_zero_is_a_valid_grade()
    {
        var r = GradeCalculator.Calculate(M(0, 0, exam: 0), NoLab);
        Assert.Equal(ModuleStatus.Complete, r.Status);
        Assert.Equal(0, r.Grade);
        Assert.False(r.Passed);
        Assert.Equal("Failed", r.StatusText);
    }

    [Fact]
    public void Zero_is_not_missing_but_null_is()
    {
        var withZero = GradeCalculator.Calculate(M(0, null), NoLab);
        Assert.Equal(ModuleStatus.Incomplete, withZero.Status);
        Assert.Equal(["CC2", "Exam"], withZero.MissingInputs);
        Assert.DoesNotContain("CC1", withZero.MissingInputs);

        // only a zero entered: not "waiting"
        var onlyZero = GradeCalculator.Calculate(M(exam: 0), NoLab);
        Assert.Equal(ModuleStatus.Incomplete, onlyZero.Status);
    }

    [Fact]
    public void Missing_coursework_requires_more_grades()
    {
        var r = GradeCalculator.Calculate(M(12), NoLab);
        Assert.Equal(ModuleStatus.Incomplete, r.Status);
        Assert.Equal(RequiredExamKind.MoreGradesRequired, r.RequiredExam.Kind);
        Assert.Contains("CC2", r.RequiredExam.Message);
        Assert.Null(r.RequiredExam.Value);
    }

    [Fact]
    public void Exam_entered_but_cc_missing_is_incomplete_without_fabricating_a_grade()
    {
        var r = GradeCalculator.Calculate(M(exam: 15), NoLab);
        Assert.Equal(ModuleStatus.Incomplete, r.Status);
        Assert.Null(r.Grade);
        Assert.Equal(RequiredExamKind.NotApplicable, r.RequiredExam.Kind);
    }

    [Fact]
    public void Lab_module_missing_lab_is_incomplete()
    {
        var r = GradeCalculator.Calculate(M(12, 14, exam: 13, hasLab: true), LabFormula);
        Assert.Equal(ModuleStatus.Incomplete, r.Status);
        Assert.Contains("Lab", r.MissingInputs);
        Assert.Null(r.Grade);
        Assert.Equal(RequiredExamKind.NotApplicable, r.RequiredExam.Kind);
    }

    [Fact]
    public void Lab_module_complete_uses_custom_formula()
    {
        var r = GradeCalculator.Calculate(M(12, 14, lab: 16, exam: 10, hasLab: true), LabFormula);
        Assert.Equal(ModuleStatus.Complete, r.Status);
        Assert.Equal(13 * 0.2 + 16 * 0.2 + 10 * 0.6, r.Grade!.Value, 10);
    }

    [Fact]
    public void Lab_module_required_exam_when_everything_else_present()
    {
        var r = GradeCalculator.Calculate(M(12, 14, lab: 16, hasLab: true), LabFormula);
        // 2.6 + 3.2 = 5.8 ; (10-5.8)/0.6 = 7
        Assert.Equal(RequiredExamKind.Needed, r.RequiredExam.Kind);
        Assert.Equal(7, r.RequiredExam.Value);
    }

    [Fact]
    public void Lab_module_missing_lab_and_exam_needs_more_grades()
    {
        var r = GradeCalculator.Calculate(M(12, 14, hasLab: true), LabFormula);
        Assert.Equal(RequiredExamKind.MoreGradesRequired, r.RequiredExam.Kind);
        Assert.Contains("Lab", r.RequiredExam.Message);
    }

    [Fact]
    public void Lab_grade_is_ignored_when_module_has_no_lab()
    {
        var r = GradeCalculator.Calculate(M(12, 14, lab: 3, exam: 13), NoLab);
        Assert.Equal(13.0, r.Grade);
    }

    [Fact]
    public void Formula_using_lab_on_a_no_lab_module_is_a_formula_error()
    {
        var r = GradeCalculator.Calculate(M(12, 14, exam: 13), LabFormula);
        Assert.Equal(ModuleStatus.FormulaError, r.Status);
        Assert.Contains("Lab", r.FormulaError);
    }

    [Fact]
    public void Missing_or_invalid_formula()
    {
        Assert.Equal(ModuleStatus.NoFormula, GradeCalculator.Calculate(M(1, 2, exam: 3), null).Status);
        Assert.Equal(ModuleStatus.NoFormula, GradeCalculator.Calculate(M(1, 2, exam: 3), "  ").Status);
        Assert.Equal(ModuleStatus.FormulaError, GradeCalculator.Calculate(M(1, 2, exam: 3), "CC1 +").Status);
    }

    [Fact]
    public void Division_by_zero_in_formula_surfaces_as_formula_error()
    {
        var r = GradeCalculator.Calculate(M(12, 14, exam: 13), "Exam / (CC1 - 12)");
        Assert.Equal(ModuleStatus.FormulaError, r.Status);
        Assert.Contains("zero", r.FormulaError);
    }

    [Theory]
    [InlineData("MAX(CC1, Exam)")]
    [InlineData("Exam * Exam")]
    [InlineData("10 / Exam")]
    public void Unsupported_structures_do_not_fabricate_required_exam(string expr)
    {
        var r = GradeCalculator.Calculate(M(12, 14), expr);
        Assert.Equal(ModuleStatus.Incomplete, r.Status);
        Assert.Equal(RequiredExamKind.Unsupported, r.RequiredExam.Kind);
        Assert.Null(r.RequiredExam.Value);
    }

    [Fact]
    public void Formula_where_exam_has_no_effect_is_unsupported()
    {
        var r = GradeCalculator.Calculate(M(12, 14), "AVG(CC1, CC2, Exam) - Exam / 3 + 0 * Exam");
        // AVG(.., Exam) contributes Exam/3 and "- Exam/3" cancels it -> no effect
        Assert.Equal(RequiredExamKind.Unsupported, r.RequiredExam.Kind);
    }

    [Fact]
    public void Formula_that_decreases_with_exam_is_unsupported()
    {
        var r = GradeCalculator.Calculate(M(12, 14), "CC1 - Exam");
        Assert.Equal(RequiredExamKind.Unsupported, r.RequiredExam.Kind);
    }

    [Fact]
    public void Custom_threshold_and_max_grade()
    {
        // out of 100, pass at 50: 0.4*avg(60,80)=28 ; need (50-28)/0.6 = 36.67
        var r = GradeCalculator.Calculate(M(60, 80, max: 100, pass: 50), NoLab);
        Assert.Equal(36.67, r.RequiredExam.Value);
        Assert.Contains("/100", r.RequiredExam.Message);
    }

    [Fact]
    public void Rounding_to_two_decimals_half_away_from_zero_and_pass_uses_rounded_grade()
    {
        // 0.4*avg(10,10) + 0.6*9.99 = 4 + 5.994 = 9.994 -> 9.99 fails
        var fail = GradeCalculator.Calculate(M(10, 10, exam: 9.99), NoLab);
        Assert.Equal(9.99, fail.Grade);
        Assert.False(fail.Passed);
        // 4 + 0.6*10.01 = 10.006 -> 10.01
        var pass = GradeCalculator.Calculate(M(10, 10, exam: 10.01), NoLab);
        Assert.Equal(10.01, pass.Grade);
        Assert.True(pass.Passed);
        // threshold exactly reached by rounding up: 4+0.6*9.992 = 9.9952 -> 10.00 passes
        var edge = GradeCalculator.Calculate(M(10, 10, exam: 9.992), NoLab);
        Assert.Equal(10.0, edge.Grade);
        Assert.True(edge.Passed);
    }

    [Fact]
    public void Decimal_grades_are_supported()
    {
        var r = GradeCalculator.Calculate(M(11.5, 12.25, exam: 13.75), NoLab);
        Assert.Equal(Math.Round(11.875 * 0.4 + 13.75 * 0.6, 2, MidpointRounding.AwayFromZero), r.Grade);
    }

    [Fact]
    public void Number_formatting_is_invariant()
    {
        Assert.Equal("8", GradeFormat.Number(8));
        Assert.Equal("8.5", GradeFormat.Number(8.5));
        Assert.Equal("9.67", GradeFormat.Number(9.666));
        Assert.Equal("—", GradeFormat.Number(null));
        Assert.Equal("8.00", GradeFormat.Fixed2(8));
    }
}
