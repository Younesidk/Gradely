using Gradly.Domain.Formulas;

namespace Gradly.Tests.Engine;

public class FormulaTests
{
    private static readonly Dictionary<string, double> Vals = new()
    {
        ["CC1"] = 12, ["CC2"] = 14, ["Lab"] = 16, ["Exam"] = 10,
    };

    private static double Eval(string text, Dictionary<string, double>? values = null)
    {
        var r = Formula.Parse(text);
        Assert.True(r.Success, r.FirstError);
        return r.Formula!.Evaluate(values ?? Vals);
    }

    [Theory]
    [InlineData("1 + 2", 3)]
    [InlineData("10 - 4 - 3", 3)]
    [InlineData("2 * 3 + 4", 10)]
    [InlineData("2 + 3 * 4", 14)]
    [InlineData("20 / 4 / 5", 1)]
    [InlineData("(2 + 3) * 4", 20)]
    [InlineData("2 * (3 + (4 - 1))", 12)]
    [InlineData("-5 + 2", -3)]
    [InlineData("--5", 5)]
    [InlineData("+5", 5)]
    [InlineData("-(2 + 3)", -5)]
    [InlineData("1.5 * 2", 3)]
    [InlineData(".5 + .5", 1)]
    [InlineData("  7   ", 7)]
    public void Arithmetic_and_parentheses(string text, double expected) =>
        Assert.Equal(expected, Eval(text), 10);

    [Fact]
    public void Default_formula_evaluates() =>
        Assert.Equal(13 * 0.4 + 10 * 0.6, Eval("(CC1 + CC2) / 2 * 0.4 + Exam * 0.6"), 10);

    [Fact]
    public void Variables_are_case_insensitive_and_substituted()
    {
        Assert.Equal(12 + 14 + 16 + 10, Eval("cc1 + CC2 + lab + EXAM"), 10);
        var f = Formula.Parse("cc1 + exam").Formula!;
        Assert.Equal(new[] { "CC1", "Exam" }, f.Variables.OrderBy(x => x));
    }

    [Theory]
    [InlineData("AVG(CC1, CC2)", 13)]
    [InlineData("AVG(CC1, CC2, Lab, Exam)", 13)]
    [InlineData("MIN(CC1, CC2, Lab)", 12)]
    [InlineData("MAX(CC1, CC2, Lab)", 16)]
    [InlineData("ABS(CC1 - Lab)", 4)]
    [InlineData("ABS(-3)", 3)]
    [InlineData("avg(2, 4)", 3)]
    [InlineData("ROUND(2.567, 2)", 2.57)]
    [InlineData("ROUND(2.5)", 3)]
    [InlineData("MAX(AVG(CC1, CC2), Exam)", 13)]
    public void Functions(string text, double expected) =>
        Assert.Equal(expected, Eval(text), 10);

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("1 +")]
    [InlineData("* 2")]
    [InlineData("(1 + 2")]
    [InlineData("1 + 2)")]
    [InlineData("1 2")]
    [InlineData("CC1 CC2")]
    [InlineData("2CC1")]
    [InlineData("1..2")]
    [InlineData("AVG(")]
    [InlineData("AVG(1,)")]
    [InlineData("()")]
    [InlineData("1 + $")]
    [InlineData("1 ^ 2")]
    [InlineData("CC1 = 2")]
    [InlineData("1e5")]
    public void Invalid_syntax_is_reported_not_thrown(string text)
    {
        var r = Formula.Parse(text);
        Assert.False(r.Success);
        Assert.NotEmpty(r.Errors);
        Assert.False(string.IsNullOrWhiteSpace(r.FirstError));
    }

    [Theory]
    [InlineData("Foo + 1", "Unknown variable")]
    [InlineData("SQRT(4)", "Unknown function")]
    [InlineData("ABS(1, 2)", "exactly 1")]
    [InlineData("AVG()", "or more")]
    [InlineData("ROUND(1, 2, 3)", "1 to 2")]
    public void Unknown_names_and_bad_arity_are_rejected(string text, string messagePart)
    {
        var r = Formula.Parse(text);
        Assert.False(r.Success);
        Assert.Contains(messagePart, r.FirstError);
    }

    [Fact]
    public void Lab_variable_rejected_when_not_allowed()
    {
        var r = Formula.Parse("CC1 + Lab", allowLab: false);
        Assert.False(r.Success);
        Assert.Contains("Lab", r.FirstError);
        Assert.True(Formula.Parse("CC1 + Lab", allowLab: true).Success);
    }

    [Fact]
    public void Division_by_zero_is_an_evaluation_error()
    {
        var f = Formula.Parse("CC1 / (CC2 - 14)").Formula!;
        Assert.False(f.TryEvaluate(Vals, out _, out var error));
        Assert.Contains("zero", error);
        Assert.Throws<FormulaEvaluationException>(() => f.Evaluate(Vals));
    }

    [Fact]
    public void Missing_variable_value_is_an_evaluation_error()
    {
        var f = Formula.Parse("CC1 + Exam").Formula!;
        var partial = new Dictionary<string, double> { ["CC1"] = 5 };
        Assert.False(f.TryEvaluate(partial, out _, out var error));
        Assert.Contains("Exam", error);
    }

    [Fact]
    public void Non_finite_results_are_errors()
    {
        var big = new string('9', 200);
        var huge = $"{big} * {big}";
        var f = Formula.Parse(huge).Formula!;
        Assert.False(f.TryEvaluate(Vals, out _, out var error));
        Assert.Contains("finite", error);
    }

    [Theory]
    [InlineData("System.IO.File.ReadAllText(\"/etc/passwd\")")]
    [InlineData("Environment.GetEnvironmentVariable(\"ADMIN_PASSWORD\")")]
    [InlineData("1; DROP TABLE Users")]
    [InlineData("CC1; Exam")]
    [InlineData("${jndi:ldap://x}")]
    [InlineData("<script>alert(1)</script>")]
    [InlineData("eval('1+1')")]
    [InlineData("CC1 + `ls`")]
    [InlineData("new Object()")]
    [InlineData("\"; var x = 1; //")]
    [InlineData("__proto__")]
    [InlineData("CC1 \\u002b Exam")]
    [InlineData("typeof(CC1)")]
    [InlineData("Exam.ToString()")]
    public void Malicious_input_is_rejected_safely(string text)
    {
        var r = Formula.Parse(text);
        Assert.False(r.Success);
        Assert.Null(r.Formula);
    }

    [Fact]
    public void Deeply_nested_input_does_not_overflow_the_stack()
    {
        var nested = new string('(', 220) + "1" + new string(')', 220);
        var r = Formula.Parse(nested);
        Assert.False(r.Success);
        Assert.Contains("nested", r.FirstError);

        var minuses = new string('-', 450) + "1";
        Assert.False(Formula.Parse(minuses).Success);

        var calls = string.Concat(Enumerable.Repeat("ABS(", 100)) + "1" + new string(')', 100);
        Assert.False(Formula.Parse(calls).Success);
    }

    [Fact]
    public void Overlong_input_is_rejected()
    {
        var text = string.Join(" + ", Enumerable.Repeat("1", 400));
        var r = Formula.Parse(text);
        Assert.False(r.Success);
        Assert.Contains("too long", r.FirstError);
    }

    [Fact]
    public void Moderate_nesting_still_works() =>
        Assert.Equal(1, Eval(new string('(', 30) + "1" + new string(')', 30)));

    [Fact]
    public void Evaluation_ignores_extra_values()
    {
        var f = Formula.Parse("CC1 * 2").Formula!;
        Assert.Equal(24, f.Evaluate(Vals));
    }

    [Fact]
    public void Linearize_extracts_coefficients()
    {
        var f = Formula.Parse("(CC1 + CC2) / 2 * 0.4 + Exam * 0.6").Formula!;
        var (a, b) = f.Linearize("Exam", new Dictionary<string, double> { ["CC1"] = 12, ["CC2"] = 14 });
        Assert.Equal(5.2, a, 10);
        Assert.Equal(0.6, b, 10);
    }

    [Theory]
    [InlineData("Exam * Exam")]
    [InlineData("10 / Exam")]
    [InlineData("MAX(Exam, 10)")]
    [InlineData("ABS(Exam - 5)")]
    [InlineData("ROUND(Exam)")]
    public void Linearize_refuses_non_linear_formulas(string text)
    {
        var f = Formula.Parse(text).Formula!;
        Assert.Throws<FormulaNotLinearException>(() => f.Linearize("Exam", new Dictionary<string, double> { ["CC1"] = 1 }));
    }
}
