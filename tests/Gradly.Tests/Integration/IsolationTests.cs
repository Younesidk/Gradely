using Gradly.Application;
using Gradly.Domain.Entities;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Tests.Integration;

/// <summary>A user must never reach another user's semesters, modules, grades or formulas.</summary>
public class IsolationTests : IClassFixture<SharedFactory>
{
    private readonly GradlyFactory _f;
    public IsolationTests(SharedFactory f) => _f = f;

    private async Task<(ApplicationUser Alice, ApplicationUser Bob, Guid SemesterId, Guid ModuleId, Guid FormulaId)> SeedAsync()
    {
        var suffix = Guid.NewGuid().ToString("N")[..8];
        var alice = await _f.CreateUserAsync("alice" + suffix);
        var bob = await _f.CreateUserAsync("bob" + suffix);
        var aliceSvc = _f.Academic(GradlyFactory.As(alice));
        var semester = await aliceSvc.CreateSemesterAsync(new SemesterForm { Name = "Alice S1" });
        var module = await aliceSvc.AddModuleAsync(semester.Id, new ModuleForm { Name = "Secret module" });
        await aliceSvc.UpdateGradesAsync(module.Id, new GradesForm(12, 14, null, 13));
        var formula = (await _f.Formulas(GradlyFactory.As(alice)).ListAsync()).First().Formula;
        return (alice, bob, semester.Id, module.Id, formula.Id);
    }

    [Fact]
    public async Task Bob_cannot_read_alices_semester_or_see_it_listed()
    {
        var (_, bob, semesterId, _, _) = await SeedAsync();
        var bobSvc = _f.Academic(GradlyFactory.As(bob));

        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.GetSemesterAsync(semesterId));
        Assert.Empty(await bobSvc.GetSemestersAsync());
        var dash = await bobSvc.GetDashboardAsync();
        Assert.Empty(dash.Semesters);
        Assert.Equal(0, dash.Overall.ModuleCount);
    }

    [Fact]
    public async Task Bob_cannot_modify_or_delete_alices_data()
    {
        var (alice, bob, semesterId, moduleId, _) = await SeedAsync();
        var bobSvc = _f.Academic(GradlyFactory.As(bob));

        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.UpdateSemesterAsync(semesterId, new SemesterForm { Name = "hacked" }));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.DeleteSemesterAsync(semesterId));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.MoveSemesterAsync(semesterId, 1));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.AddModuleAsync(semesterId, new ModuleForm { Name = "injected" }));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.UpdateModuleAsync(moduleId, new ModuleForm { Name = "hacked" }));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.UpdateGradesAsync(moduleId, new GradesForm(0, 0, null, 0)));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.SetModuleFormulaAsync(moduleId, null));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.MoveModuleAsync(moduleId, 1));
        await Assert.ThrowsAsync<NotFoundException>(() => bobSvc.DeleteModuleAsync(moduleId));

        // Alice's data is untouched.
        var view = await _f.Academic(GradlyFactory.As(alice)).GetSemesterAsync(semesterId);
        Assert.Equal("Alice S1", view.Semester.Name);
        var module = Assert.Single(view.Modules).Module;
        Assert.Equal("Secret module", module.Name);
        Assert.Equal(12, module.Cc1);
        Assert.Equal(13, module.ExamGrade);
    }

    [Fact]
    public async Task Bob_cannot_use_or_change_alices_formulas()
    {
        var (_, bob, semesterId, _, aliceFormulaId) = await SeedAsync();
        var bobFormulas = _f.Formulas(GradlyFactory.As(bob));
        var bobAcademic = _f.Academic(GradlyFactory.As(bob));

        Assert.DoesNotContain(await bobFormulas.ListAsync(), f => f.Formula.Id == aliceFormulaId);
        await Assert.ThrowsAsync<NotFoundException>(() => bobFormulas.DeleteAsync(aliceFormulaId));
        await Assert.ThrowsAsync<NotFoundException>(() => bobFormulas.SetDefaultAsync(aliceFormulaId));
        await Assert.ThrowsAsync<NotFoundException>(() =>
            bobFormulas.SaveAsync(aliceFormulaId, new FormulaForm { Name = "x", Expression = "CC1 + Exam" }));

        // Bob can't attach Alice's formula to his own module either.
        var bobSemester = await bobAcademic.CreateSemesterAsync(new SemesterForm { Name = "Bob S1" });
        var ex = await Assert.ThrowsAsync<UserFacingException>(() =>
            bobAcademic.AddModuleAsync(bobSemester.Id, new ModuleForm { Name = "M", FormulaId = aliceFormulaId }));
        Assert.Contains("isn't available", ex.Message);
        _ = semesterId;
    }

    [Fact]
    public async Task Module_queries_are_filtered_by_owner_even_when_semester_id_is_guessed()
    {
        var (alice, bob, semesterId, _, _) = await SeedAsync();
        // Simulate a forged row: a module claiming Bob as owner inside Alice's semester must never show for Alice,
        // and Alice's modules must never show for Bob.
        await using (var db = await _f.Db.CreateDbContextAsync())
        {
            db.Modules.Add(new Module { SemesterId = semesterId, UserId = bob.Id, Name = "Forged" });
            await db.SaveChangesAsync();
        }
        var aliceView = await _f.Academic(GradlyFactory.As(alice)).GetSemesterAsync(semesterId);
        Assert.DoesNotContain(aliceView.Modules, m => m.Module.Name == "Forged");
        Assert.Empty(await _f.Academic(GradlyFactory.As(bob)).GetSemestersAsync());
    }

    [Fact]
    public async Task Anonymous_callers_are_rejected()
    {
        var anon = new FakeCurrentUser();
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => _f.Academic(anon).GetSemestersAsync());
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => _f.Formulas(anon).ListAsync());
    }

    [Fact]
    public async Task Normal_users_cannot_call_admin_or_invitation_services()
    {
        var (_, bob, _, _, _) = await SeedAsync();
        var who = GradlyFactory.As(bob);
        var admin = _f.AdminSvc(who);

        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => admin.GetOverviewAsync());
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => admin.GetUsersAsync(null, 1));
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => admin.GetUserAsync(bob.Id));
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => admin.GetUserSemesterAsync(bob.Id, Guid.NewGuid()));
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => admin.SetDisabledAsync(bob.Id, true));
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => admin.SetDefaultLabFormulaAsync("CC1 + Lab + Exam"));

        var invites = _f.Invitations(who);
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => invites.CreateAsync("x@y.com"));
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => invites.ListAsync());
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => invites.RevokeAsync(Guid.NewGuid()));
        await Assert.ThrowsAsync<UnauthorizedAccessException>(() => invites.RegenerateAsync(Guid.NewGuid()));
    }

    [Fact]
    public async Task Administrator_can_inspect_any_users_records_read_only()
    {
        var (alice, _, semesterId, _, _) = await SeedAsync();
        var admin = _f.AdminSvc(_f.AsAdmin());

        var detail = await admin.GetUserAsync(alice.Id);
        Assert.Equal(alice.UserName, detail.Row.UserName);
        var semester = Assert.Single(detail.Semesters);
        Assert.Equal("Alice S1", semester.Semester.Name);
        Assert.Equal(1, detail.Row.Modules);

        var (_, view) = await admin.GetUserSemesterAsync(alice.Id, semesterId);
        Assert.Equal("Secret module", Assert.Single(view.Modules).Module.Name);

        // ...but a semester id from another user is not found under this user id.
        await Assert.ThrowsAsync<NotFoundException>(() => admin.GetUserSemesterAsync(_f.AdminIdAsync().Result, semesterId));
    }

    [Fact]
    public async Task Admin_cannot_disable_self_or_other_admins()
    {
        var admin = _f.AsAdmin();
        var svc = _f.AdminSvc(admin);
        var ex = await Assert.ThrowsAsync<UserFacingException>(() => svc.SetDisabledAsync(admin.UserId!, true));
        Assert.Contains("own account", ex.Message);

        var otherAdmin = await _f.CreateUserAsync("admin2" + Guid.NewGuid().ToString("N")[..6], admin: true);
        await Assert.ThrowsAsync<UserFacingException>(() => svc.SetDisabledAsync(otherAdmin.Id, true));
    }

    [Fact]
    public async Task Deleting_a_semester_removes_its_modules_but_not_other_users_data()
    {
        var (alice, bob, semesterId, _, _) = await SeedAsync();
        var bobSvc = _f.Academic(GradlyFactory.As(bob));
        var bobSemester = await bobSvc.CreateSemesterAsync(new SemesterForm { Name = "Bob" });
        await bobSvc.AddModuleAsync(bobSemester.Id, new ModuleForm { Name = "Bob module" });

        await _f.Academic(GradlyFactory.As(alice)).DeleteSemesterAsync(semesterId);

        await using var db = await _f.Db.CreateDbContextAsync();
        Assert.False(await db.Modules.AnyAsync(m => m.SemesterId == semesterId));
        Assert.True(await db.Modules.AnyAsync(m => m.UserId == bob.Id));
    }
}
