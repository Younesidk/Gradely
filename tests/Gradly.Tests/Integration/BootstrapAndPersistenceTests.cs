using Gradly.Application;
using Gradly.Domain.Entities;
using Gradly.Infrastructure.Data;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

namespace Gradly.Tests.Integration;

public class BootstrapAndPersistenceTests
{
    private static string NewDir() => Path.Combine(Path.GetTempPath(), "gradly-tests-" + Guid.NewGuid().ToString("N"));

    private static void Cleanup(string dir)
    {
        Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();
        try { Directory.Delete(dir, true); } catch (IOException) { }
    }

    [Fact]
    public async Task First_run_creates_the_administrator_from_configuration()
    {
        using var f = new GradlyFactory();
        var admin = await f.FindUserAsync("admin");
        Assert.NotNull(admin);
        Assert.Equal("admin@gradly.test", admin.Email);

        await using var scope = f.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        Assert.True(await users.IsInRoleAsync(admin, Roles.Admin));
        Assert.True(await users.CheckPasswordAsync(admin, GradlyFactory.AdminPassword));
        // the password is hashed, never stored as given
        Assert.DoesNotContain(GradlyFactory.AdminPassword, admin.PasswordHash);
    }

    [Fact]
    public async Task Second_startup_does_not_recreate_or_overwrite_the_administrator()
    {
        var dir = NewDir();
        try
        {
            string firstHash;
            using (var first = new GradlyFactory(dir))
            {
                var admin = await first.FindUserAsync("admin");
                firstHash = admin.PasswordHash!;

                // the admin changes their own password normally
                await using var scope = first.Services.CreateAsyncScope();
                var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
                var user = (await users.FindByNameAsync("admin"))!;
                Assert.True((await users.ChangePasswordAsync(user, GradlyFactory.AdminPassword, "Changed-By-Admin-99")).Succeeded);
                firstHash = (await users.FindByNameAsync("admin"))!.PasswordHash!;
            }
            Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();

            // restart with different bootstrap values: nothing may change
            using var second = new GradlyFactory(dir, adminPassword: "Totally-Different-1");
            await using var scope2 = second.Services.CreateAsyncScope();
            var users2 = scope2.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var admin2 = (await users2.FindByNameAsync("admin"))!;
            Assert.Equal(firstHash, admin2.PasswordHash);
            Assert.True(await users2.CheckPasswordAsync(admin2, "Changed-By-Admin-99"));
            Assert.False(await users2.CheckPasswordAsync(admin2, "Totally-Different-1"));
            Assert.Single(await users2.GetUsersInRoleAsync(Roles.Admin));
            Assert.Single(users2.Users.Where(u => u.UserName == "admin"));
        }
        finally { Cleanup(dir); }
    }

    [Fact]
    public async Task Bootstrap_never_promotes_an_existing_user_or_runs_without_all_variables()
    {
        var dir = NewDir();
        try
        {
            using (var f = new GradlyFactory(dir, bootstrapAdmin: false))
            {
                await f.CreateUserAsync("regular");
                await using var scope = f.Services.CreateAsyncScope();
                var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
                Assert.Empty(await users.GetUsersInRoleAsync(Roles.Admin)); // no env vars -> no admin
            }
            Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();

            // later start with env vars whose username matches an existing normal user: must not promote it
            using var g = new GradlyFactory(dir);
            await using var scope2 = g.Services.CreateAsyncScope();
            var users2 = scope2.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var regular = (await users2.FindByNameAsync("regular"))!;
            Assert.False(await users2.IsInRoleAsync(regular, Roles.Admin));
            Assert.Single(await users2.GetUsersInRoleAsync(Roles.Admin));
        }
        finally { Cleanup(dir); }
    }

    [Fact]
    public async Task Default_lab_formula_from_first_run_setup_is_stored_seeded_and_editable()
    {
        const string lab = "(CC1 + CC2) / 2 * 0.25 + Lab * 0.15 + Exam * 0.6"; // test value only
        using var f = new GradlyFactory(defaultLabFormula: lab);

        var admin = await f.FindUserAsync("admin");
        var adminFormulas = await f.Formulas(GradlyFactory.As(admin)).ListAsync();
        Assert.Contains(adminFormulas, x => x.Formula.ForLabExam && x.Formula.Expression == lab && x.Formula.IsDefault);

        var student = await f.CreateUserAsync("labstudent");
        var studentFormulas = await f.Formulas(GradlyFactory.As(student)).ListAsync();
        Assert.Contains(studentFormulas, x => x.Formula.ForLabExam && x.Formula.Expression == lab);

        // module with a lab exam picks the lab formula automatically and can be graded
        var academic = f.Academic(GradlyFactory.As(student));
        var sem = await academic.CreateSemesterAsync(new SemesterForm { Name = "S1" });
        var module = await academic.AddModuleAsync(sem.Id, new ModuleForm { Name = "Lab module", HasLabExam = true });
        await academic.UpdateGradesAsync(module.Id, new GradesForm(10, 10, 10, 10));
        var view = (await academic.GetSemesterAsync(sem.Id)).Modules.Single();
        Assert.Equal(10, view.Result.Grade);

        // the administrator can change it afterwards; it only affects NEW accounts
        var adminSvc = f.AdminSvc(f.AsAdmin());
        await adminSvc.SetDefaultLabFormulaAsync("CC1 * 0.1 + Lab * 0.1 + Exam * 0.8");
        Assert.Equal("CC1 * 0.1 + Lab * 0.1 + Exam * 0.8", (await adminSvc.GetOverviewAsync()).DefaultLabFormula);
        var later = await f.CreateUserAsync("laterstudent");
        Assert.Contains(await f.Formulas(GradlyFactory.As(later)).ListAsync(), x => x.Formula.Expression.StartsWith("CC1 * 0.1"));
        Assert.Contains(await f.Formulas(GradlyFactory.As(student)).ListAsync(), x => x.Formula.Expression == lab);

        // invalid replacement is refused; clearing is allowed
        await Assert.ThrowsAsync<UserFacingException>(() => adminSvc.SetDefaultLabFormulaAsync("CC1 +"));
        await adminSvc.SetDefaultLabFormulaAsync(null);
        Assert.Null((await adminSvc.GetOverviewAsync()).DefaultLabFormula);
    }

    [Fact]
    public async Task Without_a_configured_lab_formula_nothing_is_invented()
    {
        using var f = new GradlyFactory(); // no DEFAULT_LAB_FORMULA
        var student = await f.CreateUserAsync("nolab");
        var formulas = await f.Formulas(GradlyFactory.As(student)).ListAsync();
        Assert.DoesNotContain(formulas, x => x.Formula.ForLabExam);

        var academic = f.Academic(GradlyFactory.As(student));
        var sem = await academic.CreateSemesterAsync(new SemesterForm { Name = "S1" });
        var module = await academic.AddModuleAsync(sem.Id, new ModuleForm { Name = "Lab module", HasLabExam = true });
        Assert.Null(module.FormulaId);
        await academic.UpdateGradesAsync(module.Id, new GradesForm(10, 10, 10, 10));
        var view = (await academic.GetSemesterAsync(sem.Id)).Modules.Single();
        Assert.Equal(Gradly.Domain.Grading.ModuleStatus.NoFormula, view.Result.Status);
        Assert.Null(view.Result.Grade);
    }

    [Fact]
    public async Task Invalid_default_lab_formula_in_configuration_is_ignored()
    {
        using var f = new GradlyFactory(defaultLabFormula: "Lab ** nonsense(");
        Assert.NotNull(await f.FindUserAsync("admin")); // startup still succeeds
        Assert.Null(await f.AdminSvc(f.AsAdmin()).GetOverviewAsync().ContinueWith(t => t.Result.DefaultLabFormula));
    }

    [Fact]
    public async Task Data_persists_across_application_restarts()
    {
        var dir = NewDir();
        try
        {
            Guid semesterId;
            string username = "persist" + Guid.NewGuid().ToString("N")[..5];
            using (var first = new GradlyFactory(dir))
            {
                var user = await first.CreateUserAsync(username);
                var svc = first.Academic(GradlyFactory.As(user));
                var sem = await svc.CreateSemesterAsync(new SemesterForm { Name = "Persistent" });
                semesterId = sem.Id;
                var module = await svc.AddModuleAsync(sem.Id, new ModuleForm { Name = "M", Coefficient = 2.5 });
                await svc.UpdateGradesAsync(module.Id, new GradesForm(11.5, 0, null, 14.25));
            }
            Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();
            Assert.True(File.Exists(Path.Combine(dir, "gradly.db")));

            using var second = new GradlyFactory(dir);
            var user2 = await second.FindUserAsync(username);
            var view = await second.Academic(GradlyFactory.As(user2)).GetSemesterAsync(semesterId);
            var m = Assert.Single(view.Modules).Module;
            Assert.Equal("Persistent", view.Semester.Name);
            Assert.Equal(2.5, m.Coefficient);
            Assert.Equal(11.5, m.Cc1);
            Assert.Equal(0, m.Cc2);          // a real zero survives
            Assert.Null(m.LabGrade);         // a missing grade stays missing
            Assert.Equal(14.25, m.ExamGrade);
        }
        finally { Cleanup(dir); }
    }

    [Fact]
    public async Task Signed_in_sessions_survive_an_application_restart()
    {
        var dir = NewDir();
        try
        {
            string authCookie;
            using (var first = new GradlyFactory(dir))
            {
                var login = await first.NewClient().SignInAsync("admin", GradlyFactory.AdminPassword);
                Assert.True(login.IsRedirectTo("/"));
                authCookie = login.Headers.GetValues("Set-Cookie").Single(c => c.StartsWith(".Gradly.Auth=")).Split(';')[0];
                Assert.True(Directory.GetFiles(Path.Combine(dir, "keys"), "*.xml").Length > 0, "data-protection keys are persisted in the data dir");
            }
            Microsoft.Data.Sqlite.SqliteConnection.ClearAllPools();

            using var second = new GradlyFactory(dir);
            var client = second.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
            { AllowAutoRedirect = false, HandleCookies = false });
            var req = new HttpRequestMessage(HttpMethod.Get, "/admin");
            req.Headers.Add("Cookie", authCookie);
            Assert.Equal(System.Net.HttpStatusCode.OK, (await client.SendAsync(req)).StatusCode);
        }
        finally { Cleanup(dir); }
    }

    [Fact]
    public async Task Database_uses_wal_mode_and_enforces_foreign_keys()
    {
        using var f = new GradlyFactory();
        await using var db = await f.Db.CreateDbContextAsync();
        var conn = db.Database.GetDbConnection();
        await conn.OpenAsync();
        await using (var cmd = conn.CreateCommand())
        {
            cmd.CommandText = "PRAGMA journal_mode";
            Assert.Equal("wal", (await cmd.ExecuteScalarAsync())!.ToString());
        }
        await using (var cmd = conn.CreateCommand())
        {
            cmd.CommandText = "PRAGMA foreign_keys";
            Assert.Equal(1L, Convert.ToInt64(await cmd.ExecuteScalarAsync()));
        }

        // orphan rows are refused by the database itself
        db.Semesters.Add(new Semester { UserId = "no-such-user", Name = "orphan" });
        await Assert.ThrowsAsync<DbUpdateException>(() => db.SaveChangesAsync());
    }
}
