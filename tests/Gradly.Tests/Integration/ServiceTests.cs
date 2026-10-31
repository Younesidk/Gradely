using System.Net;
using System.Text.Json;
using Gradly.Application;
using Gradly.Domain.Grading;
using Gradly.Infrastructure.Email;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging.Abstractions;

namespace Gradly.Tests.Integration;

public class AcademicServiceTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public AcademicServiceTests(SharedFactory f) => _f = f;

    private async Task<(AcademicService Svc, Guid SemesterId)> NewSemesterAsync()
    {
        var user = await _f.CreateUserAsync("u" + Guid.NewGuid().ToString("N")[..8]);
        var svc = _f.Academic(GradlyFactory.As(user));
        return (svc, (await svc.CreateSemesterAsync(new SemesterForm { Name = "S1" })).Id);
    }

    [Fact]
    public async Task Zero_and_missing_grades_are_stored_differently()
    {
        var (svc, sid) = await NewSemesterAsync();
        var m = await svc.AddModuleAsync(sid, new ModuleForm { Name = "Lab", HasLabExam = true });
        Assert.Null(m.LabGrade);

        await svc.UpdateGradesAsync(m.Id, new GradesForm(null, 0, 0, null));
        var stored = (await svc.GetSemesterAsync(sid)).Modules.Single().Module;
        Assert.Null(stored.Cc1);
        Assert.Equal(0, stored.Cc2);
        Assert.Equal(0, stored.LabGrade);
        Assert.Null(stored.ExamGrade);
    }

    [Theory]
    [InlineData(-0.01)]
    [InlineData(20.01)]
    [InlineData(double.NaN)]
    [InlineData(double.PositiveInfinity)]
    public async Task Grades_outside_the_modules_range_are_rejected(double bad)
    {
        var (svc, sid) = await NewSemesterAsync();
        var m = await svc.AddModuleAsync(sid, new ModuleForm { Name = "M" });
        await Assert.ThrowsAsync<UserFacingException>(() => svc.UpdateGradesAsync(m.Id, new GradesForm(bad, null, null, null)));
        await Assert.ThrowsAsync<UserFacingException>(() => svc.UpdateGradesAsync(m.Id, new GradesForm(null, null, null, bad)));
        // boundaries are accepted
        await svc.UpdateGradesAsync(m.Id, new GradesForm(0, 20, null, 20));
    }

    [Fact]
    public async Task Custom_max_grade_changes_the_valid_range()
    {
        var (svc, sid) = await NewSemesterAsync();
        var m = await svc.AddModuleAsync(sid, new ModuleForm { Name = "Out of 100", MaxGrade = 100, PassingThreshold = 50 });
        await svc.UpdateGradesAsync(m.Id, new GradesForm(85, 60, null, 70));
        // lowering the maximum below an entered grade is refused
        await Assert.ThrowsAsync<UserFacingException>(() =>
            svc.UpdateModuleAsync(m.Id, new ModuleForm { Name = "Out of 100", MaxGrade = 20, PassingThreshold = 10 }));
    }

    [Theory]
    [InlineData("", 1, 20, 10)]
    [InlineData("   ", 1, 20, 10)]
    [InlineData("M", -1, 20, 10)]
    [InlineData("M", 1, 0, 0)]
    [InlineData("M", 1, 20, 21)]
    [InlineData("M", 1, 20, -1)]
    [InlineData("M", 1001, 20, 10)]
    public async Task Invalid_module_settings_are_rejected(string name, double coeff, double max, double pass)
    {
        var (svc, sid) = await NewSemesterAsync();
        await Assert.ThrowsAsync<UserFacingException>(() =>
            svc.AddModuleAsync(sid, new ModuleForm { Name = name, Coefficient = coeff, MaxGrade = max, PassingThreshold = pass }));
    }

    [Fact]
    public async Task Semester_dates_must_be_ordered()
    {
        var user = await _f.CreateUserAsync("d" + Guid.NewGuid().ToString("N")[..8]);
        var svc = _f.Academic(GradlyFactory.As(user));
        await Assert.ThrowsAsync<UserFacingException>(() => svc.CreateSemesterAsync(new SemesterForm
        { Name = "S", StartDate = new DateOnly(2027, 6, 1), EndDate = new DateOnly(2027, 1, 1) }));
        var ok = await svc.CreateSemesterAsync(new SemesterForm
        { Name = "S", AcademicYear = "2026-2027", StartDate = new DateOnly(2026, 9, 1), EndDate = new DateOnly(2027, 1, 31) });
        Assert.Equal(new DateOnly(2026, 9, 1), ok.StartDate);
    }

    [Fact]
    public async Task Modules_and_semesters_can_be_reordered()
    {
        var user = await _f.CreateUserAsync("r" + Guid.NewGuid().ToString("N")[..8]);
        var svc = _f.Academic(GradlyFactory.As(user));
        var a = await svc.CreateSemesterAsync(new SemesterForm { Name = "A" });
        var b = await svc.CreateSemesterAsync(new SemesterForm { Name = "B" });
        var c = await svc.CreateSemesterAsync(new SemesterForm { Name = "C" });
        await svc.MoveSemesterAsync(c.Id, -1);
        Assert.Equal(["A", "C", "B"], (await svc.GetSemestersAsync()).Select(s => s.Semester.Name));
        await svc.MoveSemesterAsync(a.Id, -1); // already first: no-op
        await svc.MoveSemesterAsync(b.Id, 1);  // already last: no-op
        Assert.Equal(["A", "C", "B"], (await svc.GetSemestersAsync()).Select(s => s.Semester.Name));

        var m1 = await svc.AddModuleAsync(a.Id, new ModuleForm { Name = "M1" });
        var m2 = await svc.AddModuleAsync(a.Id, new ModuleForm { Name = "M2" });
        await svc.AddModuleAsync(a.Id, new ModuleForm { Name = "M3" });
        await svc.MoveModuleAsync(m2.Id, -1);
        Assert.Equal(["M2", "M1", "M3"], (await svc.GetSemesterAsync(a.Id)).Modules.Select(m => m.Module.Name));
        await svc.MoveModuleAsync(m1.Id, 1);
        Assert.Equal(["M2", "M3", "M1"], (await svc.GetSemesterAsync(a.Id)).Modules.Select(m => m.Module.Name));
    }

    [Fact]
    public async Task Semester_summary_uses_coefficients_and_flags_provisional_results()
    {
        var (svc, sid) = await NewSemesterAsync();
        var big = await svc.AddModuleAsync(sid, new ModuleForm { Name = "Big", Coefficient = 3 });
        var small = await svc.AddModuleAsync(sid, new ModuleForm { Name = "Small", Coefficient = 1 });
        var open = await svc.AddModuleAsync(sid, new ModuleForm { Name = "Open", Coefficient = 5 });
        await svc.UpdateGradesAsync(big.Id, new GradesForm(12, 14, null, 13));   // 13
        await svc.UpdateGradesAsync(small.Id, new GradesForm(8, 8, null, 8));    // 8
        await svc.UpdateGradesAsync(open.Id, new GradesForm(10, 10, null, null));

        var s = (await svc.GetSemesterAsync(sid)).Summary;
        Assert.Equal(11.75, s.Average);
        Assert.Equal(3, s.ModuleCount);
        Assert.Equal(2, s.CompletedCount);
        Assert.Equal(1, s.IncompleteCount);
        Assert.Equal(1, s.PassedCount);
        Assert.Equal(1, s.FailedCount);
        Assert.True(s.IsProvisional);

        var dash = await svc.GetDashboardAsync();
        Assert.Equal(11.75, dash.Overall.Average);
        Assert.Equal(sid, dash.Current!.Semester.Id);
    }

    [Fact]
    public async Task Module_formula_must_match_the_lab_kind_and_toggling_lab_reassigns_it()
    {
        var user = await _f.CreateUserAsync("k" + Guid.NewGuid().ToString("N")[..8]);
        var who = GradlyFactory.As(user);
        var svc = _f.Academic(who);
        var formulas = _f.Formulas(who);
        var labFormula = await formulas.SaveAsync(null, new FormulaForm { Name = "Lab one", Expression = "CC1 + CC2 + Lab + Exam", ForLabExam = true });
        var noLab = (await formulas.ListAsync()).First(f => !f.Formula.ForLabExam).Formula;

        var sem = await svc.CreateSemesterAsync(new SemesterForm { Name = "S" });
        await Assert.ThrowsAsync<UserFacingException>(() =>
            svc.AddModuleAsync(sem.Id, new ModuleForm { Name = "Wrong", HasLabExam = false, FormulaId = labFormula.Id }));

        var m = await svc.AddModuleAsync(sem.Id, new ModuleForm { Name = "M" });
        Assert.Equal(noLab.Id, m.FormulaId);

        // turn the lab exam on without naming a formula: falls back to the user's lab default
        await svc.UpdateModuleAsync(m.Id, new ModuleForm { Name = "M", HasLabExam = true });
        Assert.Equal(labFormula.Id, (await svc.GetSemesterAsync(sem.Id)).Modules.Single().Module.FormulaId);
        // and back
        await svc.UpdateModuleAsync(m.Id, new ModuleForm { Name = "M", HasLabExam = false });
        Assert.Equal(noLab.Id, (await svc.GetSemesterAsync(sem.Id)).Modules.Single().Module.FormulaId);
    }

    [Fact]
    public async Task Switching_a_module_to_another_formula_recalculates_its_grade()
    {
        var user = await _f.CreateUserAsync("sw" + Guid.NewGuid().ToString("N")[..8]);
        var who = GradlyFactory.As(user);
        var svc = _f.Academic(who);
        var custom = await _f.Formulas(who).SaveAsync(null, new FormulaForm { Name = "Exam only", Expression = "Exam" });
        var sem = await svc.CreateSemesterAsync(new SemesterForm { Name = "S" });
        var m = await svc.AddModuleAsync(sem.Id, new ModuleForm { Name = "M" });
        await svc.UpdateGradesAsync(m.Id, new GradesForm(10, 10, null, 16));
        Assert.Equal(13.6, (await svc.GetSemesterAsync(sem.Id)).Modules.Single().Result.Grade);

        await svc.SetModuleFormulaAsync(m.Id, custom.Id);
        Assert.Equal(16, (await svc.GetSemesterAsync(sem.Id)).Modules.Single().Result.Grade);
    }
}

public class FormulaServiceTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public FormulaServiceTests(SharedFactory f) => _f = f;

    private async Task<(FormulaService Formulas, AcademicService Academic)> NewUserAsync()
    {
        var user = await _f.CreateUserAsync("f" + Guid.NewGuid().ToString("N")[..8]);
        var who = GradlyFactory.As(user);
        return (_f.Formulas(who), _f.Academic(who));
    }

    [Fact]
    public async Task New_accounts_get_the_default_no_lab_formula()
    {
        var (formulas, _) = await NewUserAsync();
        var f = (await formulas.ListAsync()).Single(x => !x.Formula.ForLabExam).Formula;
        Assert.Equal("(CC1 + CC2) / 2 * 0.4 + Exam * 0.6", f.Expression);
        Assert.True(f.IsDefault);
    }

    [Theory]
    [InlineData("CC1 +", false)]
    [InlineData("Lab + Exam", false)]       // Lab is not available without a lab exam
    [InlineData("", true)]
    [InlineData("42", true)]                // must use at least one variable
    [InlineData("Foo(Exam)", true)]
    [InlineData("System.Diagnostics.Process.Start(\"calc\")", true)]
    public async Task Invalid_formulas_cannot_be_saved(string expression, bool forLab)
    {
        var (formulas, _) = await NewUserAsync();
        await Assert.ThrowsAsync<UserFacingException>(() =>
            formulas.SaveAsync(null, new FormulaForm { Name = "bad", Expression = expression, ForLabExam = forLab }));
    }

    [Fact]
    public async Task Formula_names_are_unique_per_user_but_not_across_users()
    {
        var (formulas, _) = await NewUserAsync();
        await formulas.SaveAsync(null, new FormulaForm { Name = "Mine", Expression = "CC1 + Exam" });
        await Assert.ThrowsAsync<UserFacingException>(() =>
            formulas.SaveAsync(null, new FormulaForm { Name = "mine", Expression = "CC2 + Exam" }));
        var (other, _) = await NewUserAsync();
        await other.SaveAsync(null, new FormulaForm { Name = "Mine", Expression = "CC1 + Exam" });
    }

    [Fact]
    public async Task Formula_in_use_cannot_be_deleted_until_modules_are_reassigned()
    {
        var (formulas, academic) = await NewUserAsync();
        var custom = await formulas.SaveAsync(null, new FormulaForm { Name = "Custom", Expression = "(CC1 + CC2 + Exam) / 3" });
        var sem = await academic.CreateSemesterAsync(new SemesterForm { Name = "S" });
        var module = await academic.AddModuleAsync(sem.Id, new ModuleForm { Name = "M", FormulaId = custom.Id });

        var ex = await Assert.ThrowsAsync<UserFacingException>(() => formulas.DeleteAsync(custom.Id));
        Assert.Contains("1 module", ex.Message);
        Assert.Equal(1, (await formulas.ListAsync()).Single(x => x.Formula.Id == custom.Id).ModulesUsing);

        var standard = (await formulas.ListAsync()).First(x => x.Formula.Name == FormulaService.DefaultNoLabName).Formula;
        await academic.SetModuleFormulaAsync(module.Id, standard.Id);
        await formulas.DeleteAsync(custom.Id);
        Assert.DoesNotContain(await formulas.ListAsync(), x => x.Formula.Id == custom.Id);
    }

    [Fact]
    public async Task Default_formula_is_exclusive_per_kind()
    {
        var (formulas, _) = await NewUserAsync();
        var second = await formulas.SaveAsync(null, new FormulaForm { Name = "Second", Expression = "CC1 + CC2 + Exam", IsDefault = true });
        var list = await formulas.ListAsync();
        Assert.Equal(second.Id, Assert.Single(list, x => !x.Formula.ForLabExam && x.Formula.IsDefault).Formula.Id);

        var first = list.First(x => x.Formula.Name == FormulaService.DefaultNoLabName).Formula;
        await formulas.SetDefaultAsync(first.Id);
        Assert.Equal(first.Id, Assert.Single(await formulas.ListAsync(), x => x.Formula.IsDefault).Formula.Id);
    }

    [Fact]
    public async Task Editing_a_formula_changes_grades_of_modules_that_use_it_but_its_kind_is_locked()
    {
        var (formulas, academic) = await NewUserAsync();
        var f = await formulas.SaveAsync(null, new FormulaForm { Name = "Edit me", Expression = "Exam" });
        var sem = await academic.CreateSemesterAsync(new SemesterForm { Name = "S" });
        var m = await academic.AddModuleAsync(sem.Id, new ModuleForm { Name = "M", FormulaId = f.Id });
        await academic.UpdateGradesAsync(m.Id, new GradesForm(10, 10, null, 14));
        Assert.Equal(14, (await academic.GetSemesterAsync(sem.Id)).Modules.Single().Result.Grade);

        await formulas.SaveAsync(f.Id, new FormulaForm { Name = "Edit me", Expression = "(CC1 + Exam) / 2" });
        Assert.Equal(12, (await academic.GetSemesterAsync(sem.Id)).Modules.Single().Result.Grade);

        await Assert.ThrowsAsync<UserFacingException>(() =>
            formulas.SaveAsync(f.Id, new FormulaForm { Name = "Edit me", Expression = "CC1 + Lab + Exam", ForLabExam = true }));
    }
}

public class StorageEstimatorTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public StorageEstimatorTests(SharedFactory f) => _f = f;

    [Fact]
    public async Task Estimates_are_per_user_grow_with_data_and_are_cached()
    {
        var small = await _f.CreateUserAsync("small" + Guid.NewGuid().ToString("N")[..5]);
        var big = await _f.CreateUserAsync("big" + Guid.NewGuid().ToString("N")[..5]);
        var bigSvc = _f.Academic(GradlyFactory.As(big));
        for (var s = 0; s < 3; s++)
        {
            var sem = await bigSvc.CreateSemesterAsync(new SemesterForm { Name = "Semester " + s, AcademicYear = "2026-2027" });
            for (var m = 0; m < 6; m++) await bigSvc.AddModuleAsync(sem.Id, new ModuleForm { Name = $"Module number {m}", Code = $"M{m}" });
        }

        var estimator = _f.NewStorageEstimator();
        var report = await estimator.GetAsync();
        Assert.True(report.For(small.Id) > 0, "even an empty account has an identity record");
        Assert.True(report.For(big.Id) > report.For(small.Id));
        Assert.Equal(18, report.PerUser[big.Id].Modules);
        Assert.Equal(3, report.PerUser[big.Id].Semesters);
        Assert.Equal(report.PerUser.Values.Sum(u => u.Bytes), report.TotalEstimatedBytes);
        Assert.Equal(big.Id, report.Largest(1).First().UserId);
        Assert.True(report.DatabaseFileBytes > 0);

        // cached: more data does not show until the cache is bypassed
        await bigSvc.CreateSemesterAsync(new SemesterForm { Name = "Later" });
        Assert.Same(report, await estimator.GetAsync());
        var fresh = await estimator.GetAsync(forceRefresh: true);
        Assert.NotSame(report, fresh);
        Assert.True(fresh.For(big.Id) > report.For(big.Id));
    }

    [Fact]
    public async Task Admin_overview_and_user_list_expose_storage_estimates()
    {
        var user = await _f.CreateUserAsync("shown" + Guid.NewGuid().ToString("N")[..5]);
        var svc = _f.Academic(GradlyFactory.As(user));
        var sem = await svc.CreateSemesterAsync(new SemesterForm { Name = "S" });
        await svc.AddModuleAsync(sem.Id, new ModuleForm { Name = "M" });

        var admin = _f.AdminSvc(_f.AsAdmin());
        var overview = await admin.GetOverviewAsync(refreshStorage: true);
        Assert.True(overview.Storage.TotalEstimatedBytes > 0);
        Assert.NotEmpty(overview.LargestUsers);

        var page = await admin.GetUsersAsync(user.UserName, 1);
        var row = Assert.Single(page.Items);
        Assert.Equal(1, row.Semesters);
        Assert.Equal(1, row.Modules);
        Assert.True(row.StorageBytes > 0);
    }

    [Fact]
    public async Task User_search_and_paging_work_and_escape_wildcards()
    {
        var tag = "pg" + Guid.NewGuid().ToString("N")[..4];
        for (var i = 0; i < 7; i++) await _f.CreateUserAsync($"{tag}_user{i}");
        var admin = _f.AdminSvc(_f.AsAdmin());

        var p1 = await admin.GetUsersAsync(tag, 1, 5);
        Assert.Equal(7, p1.Total);
        Assert.Equal(5, p1.Items.Count);
        Assert.Equal(2, p1.TotalPages);
        var p2 = await admin.GetUsersAsync(tag, 2, 5);
        Assert.Equal(2, p2.Items.Count);
        Assert.DoesNotContain((await admin.GetUsersAsync("%", 1)).Items, u => !u.UserName.Contains((char)37));
        Assert.Equal(0, (await admin.GetUsersAsync("no-such-user-xyz", 1)).Total);
        Assert.Equal(2, (await admin.GetUsersAsync(tag, 99, 5)).PageNumber); // clamps
    }
}

public class ResendEmailServiceTests
{
    private sealed class StubHandler(HttpStatusCode status, Exception? throws = null) : HttpMessageHandler
    {
        public HttpRequestMessage? Request { get; private set; }
        public string? Body { get; private set; }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct)
        {
            Request = request;
            Body = request.Content is null ? null : await request.Content.ReadAsStringAsync(ct);
            if (throws is not null) throw throws;
            return new HttpResponseMessage(status) { Content = new StringContent("{}") };
        }
    }

    private static ResendEmailService Make(StubHandler h, string? key = "re_test_key", string? from = "Gradly <no-reply@example.com>") =>
        new(new HttpClient(h) { BaseAddress = new Uri("https://api.resend.com/") },
            new GradlyOptions { ResendApiKey = key, ResendFromEmail = from, BaseUrl = "http://localhost:5103" },
            NullLogger<ResendEmailService>.Instance);

    [Fact]
    public async Task Sends_a_bearer_authenticated_json_request_to_resend()
    {
        var h = new StubHandler(HttpStatusCode.OK);
        var svc = Make(h);
        var msg = EmailTemplates.Invitation("student@uni.edu", "http://localhost:5103/account/register?token=abc", DateTime.UtcNow.AddDays(7));
        var result = await svc.SendAsync(msg);

        Assert.True(result.Success);
        Assert.Equal("https://api.resend.com/emails", h.Request!.RequestUri!.ToString());
        Assert.Equal(HttpMethod.Post, h.Request.Method);
        Assert.Equal("Bearer re_test_key", h.Request.Headers.Authorization!.ToString());
        using var json = JsonDocument.Parse(h.Body!);
        Assert.Equal("Gradly <no-reply@example.com>", json.RootElement.GetProperty("from").GetString());
        Assert.Equal("student@uni.edu", json.RootElement.GetProperty("to")[0].GetString());
        Assert.Contains("token=abc", json.RootElement.GetProperty("html").GetString());
        Assert.Contains("token=abc", json.RootElement.GetProperty("text").GetString());
    }

    [Fact]
    public async Task Does_nothing_when_not_configured()
    {
        var h = new StubHandler(HttpStatusCode.OK);
        foreach (var (key, from) in new (string?, string?)[] { (null, null), ("key", null), (null, "a@b.c"), ("", "a@b.c") })
        {
            var svc = Make(h, key, from);
            Assert.False(svc.IsConfigured);
            var res = await svc.SendAsync(new EmailMessage("a@b.c", "s", "h", "t"));
            Assert.False(res.Success);
        }
        Assert.Null(h.Request);
    }

    [Theory]
    [InlineData(HttpStatusCode.Unauthorized)]
    [InlineData(HttpStatusCode.UnprocessableEntity)]
    [InlineData(HttpStatusCode.InternalServerError)]
    public async Task Provider_errors_are_reported_without_throwing_or_leaking_the_key(HttpStatusCode status)
    {
        var res = await Make(new StubHandler(status)).SendAsync(new EmailMessage("a@b.c", "s", "h", "t"));
        Assert.False(res.Success);
        Assert.DoesNotContain("re_test_key", res.Error);
    }

    [Fact]
    public async Task Network_failures_are_reported_without_throwing()
    {
        var res = await Make(new StubHandler(HttpStatusCode.OK, new HttpRequestException("boom re_test_key"))).SendAsync(new EmailMessage("a@b.c", "s", "h", "t"));
        Assert.False(res.Success);
        Assert.DoesNotContain("re_test_key", res.Error);
    }

    [Fact]
    public void Templates_html_encode_untrusted_values()
    {
        var msg = EmailTemplates.Invitation("x\"><script>alert(1)</script>@evil.com", "http://h/register?token=a&b=\"c", DateTime.UtcNow.AddDays(7));
        Assert.DoesNotContain("<script>", msg.Html);
        Assert.Contains("Your grades. Your rules.", msg.Html);
    }
}

public class AccountServiceTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public AccountServiceTests(SharedFactory f) => _f = f;

    private AccountService Svc(FakeCurrentUser who, IEmailService? email = null) =>
        new(_f.Services.GetRequiredService<IServiceScopeFactory>(), who, email ?? _f.Email,
            _f.Services.GetRequiredService<GradlyOptions>());

    [Fact]
    public async Task Username_and_password_changes_work_and_validate()
    {
        var user = await _f.CreateUserAsync("acct" + Guid.NewGuid().ToString("N")[..6]);
        var svc = Svc(GradlyFactory.As(user));
        var newName = "renamed" + Guid.NewGuid().ToString("N")[..6];

        await svc.ChangeUserNameAsync(newName);
        Assert.Equal(newName, (await svc.GetAsync()).UserName);
        await Assert.ThrowsAsync<UserFacingException>(() => svc.ChangeUserNameAsync("admin")); // taken
        await Assert.ThrowsAsync<UserFacingException>(() => svc.ChangeUserNameAsync("  "));
        await Assert.ThrowsAsync<UserFacingException>(() => svc.ChangeUserNameAsync("bad name!"));

        var wrong = await Assert.ThrowsAsync<UserFacingException>(() => svc.ChangePasswordAsync("not-my-password", "Brand-New-Pass-1"));
        Assert.Contains("incorrect", wrong.Message);
        await Assert.ThrowsAsync<UserFacingException>(() => svc.ChangePasswordAsync(GradlyFactory.UserPassword, "short"));
        await svc.ChangePasswordAsync(GradlyFactory.UserPassword, "Brand-New-Pass-1");

        Assert.True((await _f.NewClient().SignInAsync(newName, "Brand-New-Pass-1")).IsRedirectTo("/"));
        Assert.False((await _f.NewClient().SignInAsync(newName, GradlyFactory.UserPassword)).IsRedirectTo("/"));
    }

    [Fact]
    public async Task Email_change_requires_confirmation_through_the_emailed_link()
    {
        var oldEmail = $"old{Guid.NewGuid():N}"[..14] + "@gradly.test";
        var newEmail = $"new{Guid.NewGuid():N}"[..14] + "@gradly.test";
        var user = await _f.CreateUserAsync("mail" + Guid.NewGuid().ToString("N")[..6], oldEmail);
        var email = new FakeEmail { IsConfigured = true };
        var svc = Svc(GradlyFactory.As(user), email);

        var req = await svc.RequestEmailChangeAsync(newEmail);
        Assert.True(req.EmailSent);
        Assert.Null(req.ManualLink);
        Assert.Equal(oldEmail, (await svc.GetAsync()).Email); // unchanged until confirmed
        var sent = Assert.Single(email.Sent);
        Assert.Equal(newEmail, sent.To);

        var link = System.Text.RegularExpressions.Regex.Match(sent.Text, @"http\S+").Value;
        var res = await _f.NewClient().GetAsync(new Uri(link).PathAndQuery);
        Assert.Equal(HttpStatusCode.OK, res.StatusCode);
        Assert.Contains("confirmed", await res.Content.ReadAsStringAsync());
        Assert.Equal(newEmail, (await svc.GetAsync()).Email);

        // a tampered link does nothing
        var other = await _f.CreateUserAsync("mail2" + Guid.NewGuid().ToString("N")[..6]);
        var tampered = new Uri(link).PathAndQuery.Replace(user.Id, other.Id);
        var bad = await (await _f.NewClient().GetAsync(tampered)).Content.ReadAsStringAsync();
        Assert.Contains("invalid or has expired", bad);
    }

    [Fact]
    public async Task Without_email_delivery_the_confirmation_link_is_returned_to_the_user()
    {
        var user = await _f.CreateUserAsync("manual" + Guid.NewGuid().ToString("N")[..6]);
        var req = await Svc(GradlyFactory.As(user), new FakeEmail { IsConfigured = false })
            .RequestEmailChangeAsync($"fresh{Guid.NewGuid():N}"[..14] + "@gradly.test");
        Assert.False(req.EmailSent);
        Assert.Contains("/account/confirm-email-change?", req.ManualLink);
        await Assert.ThrowsAsync<UserFacingException>(() => Svc(GradlyFactory.As(user)).RequestEmailChangeAsync("not an email"));
    }
}
