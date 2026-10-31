using System.ComponentModel.DataAnnotations;
using Gradly.Domain.Entities;
using Gradly.Domain.Grading;
using Gradly.Infrastructure.Data;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Application;

public sealed class SemesterForm
{
    [Required(ErrorMessage = "Give the semester a name."), StringLength(100)]
    public string Name { get; set; } = "";

    [StringLength(50)]
    public string? AcademicYear { get; set; }

    public DateOnly? StartDate { get; set; }
    public DateOnly? EndDate { get; set; }
}

public sealed class ModuleForm
{
    [Required(ErrorMessage = "Give the module a name."), StringLength(100)]
    public string Name { get; set; } = "";

    [StringLength(30)]
    public string? Code { get; set; }

    public double Coefficient { get; set; } = 1;
    public double MaxGrade { get; set; } = 20;
    public double PassingThreshold { get; set; } = 10;
    public bool HasLabExam { get; set; }
    public Guid? FormulaId { get; set; }
}

/// <summary>The four editable grades of a module. null = not entered.</summary>
public sealed record GradesForm(double? Cc1, double? Cc2, double? Lab, double? Exam);

public sealed record ModuleView(Module Module, GradeFormula? Formula, ModuleResult Result)
{
    public ModuleScore Score => new(Module.Coefficient, Module.MaxGrade, Result);
}

public sealed record SemesterView(Semester Semester, IReadOnlyList<ModuleView> Modules, SemesterSummary Summary);

public sealed record DashboardData(IReadOnlyList<SemesterView> Semesters, SemesterSummary Overall, SemesterView? Current);

/// <summary>Shared read model. Callers are responsible for authorising <paramref name="userId"/>.</summary>
public static class AcademicReader
{
    public static ModuleView BuildModule(Module m, IReadOnlyDictionary<Guid, GradeFormula> formulas)
    {
        GradeFormula? f = null;
        if (m.FormulaId is { } fid) formulas.TryGetValue(fid, out f);
        var result = GradeCalculator.Calculate(
            new ModuleInput(m.Cc1, m.Cc2, m.LabGrade, m.ExamGrade, m.HasLabExam, m.MaxGrade, m.PassingThreshold),
            f?.Expression);
        return new ModuleView(m, f, result);
    }

    public static async Task<List<SemesterView>> LoadAsync(AppDbContext db, string userId, Guid? semesterId, CancellationToken ct)
    {
        var semesters = db.Semesters.AsNoTracking().Where(s => s.UserId == userId);
        if (semesterId is { } sid) semesters = semesters.Where(s => s.Id == sid);
        var semesterList = await semesters.OrderBy(s => s.SortOrder).ThenBy(s => s.CreatedAtUtc).ToListAsync(ct);
        if (semesterList.Count == 0) return [];

        var ids = semesterList.Select(s => s.Id).ToList();
        var modules = await db.Modules.AsNoTracking()
            .Where(m => m.UserId == userId && ids.Contains(m.SemesterId))
            .OrderBy(m => m.SortOrder).ThenBy(m => m.Name).ToListAsync(ct);
        var formulas = await db.Formulas.AsNoTracking().Where(f => f.UserId == userId)
            .ToDictionaryAsync(f => f.Id, ct);

        return semesterList.Select(s =>
        {
            var views = modules.Where(m => m.SemesterId == s.Id).Select(m => BuildModule(m, formulas)).ToList();
            return new SemesterView(s, views, SemesterCalculator.Summarize(views.Select(v => v.Score).ToList()));
        }).ToList();
    }

    public static DashboardData BuildDashboard(IReadOnlyList<SemesterView> semesters)
    {
        var all = semesters.SelectMany(s => s.Modules).Select(m => m.Score).ToList();
        var current = semesters
            .OrderByDescending(s => Math.Max(s.Semester.UpdatedAtUtc.Ticks,
                s.Modules.Count == 0 ? 0 : s.Modules.Max(m => m.Module.UpdatedAtUtc.Ticks)))
            .FirstOrDefault();
        return new DashboardData(semesters, SemesterCalculator.Summarize(all), current);
    }
}

/// <summary>
/// Semesters and modules of the signed-in user. The user id always comes from <see cref="ICurrentUser"/>
/// and every query filters on it, so ids belonging to other users simply resolve to "not found".
/// </summary>
public sealed class AcademicService(IDbContextFactory<AppDbContext> dbFactory, ICurrentUser currentUser, TimeProvider clock)
{
    private DateTime Now => clock.GetUtcNow().UtcDateTime;

    // ---------- reads ----------

    public async Task<DashboardData> GetDashboardAsync(CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        return AcademicReader.BuildDashboard(await AcademicReader.LoadAsync(db, userId, null, ct));
    }

    public async Task<List<SemesterView>> GetSemestersAsync(CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        return await AcademicReader.LoadAsync(db, userId, null, ct);
    }

    public async Task<SemesterView> GetSemesterAsync(Guid id, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        return (await AcademicReader.LoadAsync(db, userId, id, ct)).FirstOrDefault()
               ?? throw new NotFoundException("That semester");
    }

    public async Task<List<GradeFormula>> GetFormulasAsync(CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        return await db.Formulas.AsNoTracking().Where(f => f.UserId == userId)
            .OrderBy(f => f.ForLabExam).ThenBy(f => f.Name).ToListAsync(ct);
    }

    // ---------- semesters ----------

    public async Task<Semester> CreateSemesterAsync(SemesterForm form, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        ValidateSemester(form);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var next = (await db.Semesters.Where(s => s.UserId == userId).MaxAsync(s => (int?)s.SortOrder, ct) ?? -1) + 1;
        var s = new Semester
        {
            UserId = userId, SortOrder = next, CreatedAtUtc = Now, UpdatedAtUtc = Now,
            Name = form.Name.Trim(), AcademicYear = Clean(form.AcademicYear),
            StartDate = form.StartDate, EndDate = form.EndDate,
        };
        db.Semesters.Add(s);
        await db.SaveChangesAsync(ct);
        return s;
    }

    public async Task UpdateSemesterAsync(Guid id, SemesterForm form, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        ValidateSemester(form);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var s = await db.Semesters.FirstOrDefaultAsync(x => x.Id == id && x.UserId == userId, ct)
                ?? throw new NotFoundException("That semester");
        s.Name = form.Name.Trim();
        s.AcademicYear = Clean(form.AcademicYear);
        s.StartDate = form.StartDate;
        s.EndDate = form.EndDate;
        s.UpdatedAtUtc = Now;
        await db.SaveChangesAsync(ct);
    }

    public async Task DeleteSemesterAsync(Guid id, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        await using var tx = await db.Database.BeginTransactionAsync(ct);
        var s = await db.Semesters.FirstOrDefaultAsync(x => x.Id == id && x.UserId == userId, ct)
                ?? throw new NotFoundException("That semester");
        db.Semesters.Remove(s); // modules cascade
        await db.SaveChangesAsync(ct);
        await tx.CommitAsync(ct);
    }

    public async Task MoveSemesterAsync(Guid id, int direction, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var list = await db.Semesters.Where(s => s.UserId == userId)
            .OrderBy(s => s.SortOrder).ThenBy(s => s.CreatedAtUtc).ToListAsync(ct);
        if (!Reorder(list, s => s.Id == id, direction, (s, i) => s.SortOrder = i)) return;
        await db.SaveChangesAsync(ct);
    }

    // ---------- modules ----------

    public async Task<Module> AddModuleAsync(Guid semesterId, ModuleForm form, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        ValidateModule(form);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var semester = await db.Semesters.FirstOrDefaultAsync(s => s.Id == semesterId && s.UserId == userId, ct)
                       ?? throw new NotFoundException("That semester");

        var next = (await db.Modules.Where(m => m.SemesterId == semesterId && m.UserId == userId)
            .MaxAsync(m => (int?)m.SortOrder, ct) ?? -1) + 1;
        var module = new Module { SemesterId = semesterId, UserId = userId, SortOrder = next };
        Apply(module, form);
        module.FormulaId = await ResolveFormulaAsync(db, userId, form.FormulaId, form.HasLabExam, ct);
        module.UpdatedAtUtc = Now;
        semester.UpdatedAtUtc = Now;
        db.Modules.Add(module);
        await db.SaveChangesAsync(ct);
        return module;
    }

    public async Task UpdateModuleAsync(Guid moduleId, ModuleForm form, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        ValidateModule(form);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var module = await db.Modules.FirstOrDefaultAsync(m => m.Id == moduleId && m.UserId == userId, ct)
                     ?? throw new NotFoundException("That module");
        ValidateGradesWithin(module.Cc1, module.Cc2, module.HasLabExam || form.HasLabExam ? module.LabGrade : null,
            module.ExamGrade, form.MaxGrade);
        Apply(module, form);
        module.FormulaId = await ResolveFormulaAsync(db, userId, form.FormulaId, form.HasLabExam, ct);
        module.UpdatedAtUtc = Now;
        await TouchSemesterAsync(db, module.SemesterId, userId, ct);
        await db.SaveChangesAsync(ct);
    }

    public async Task UpdateGradesAsync(Guid moduleId, GradesForm grades, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var module = await db.Modules.FirstOrDefaultAsync(m => m.Id == moduleId && m.UserId == userId, ct)
                     ?? throw new NotFoundException("That module");
        ValidateGradesWithin(grades.Cc1, grades.Cc2, module.HasLabExam ? grades.Lab : null, grades.Exam, module.MaxGrade);
        module.Cc1 = grades.Cc1;
        module.Cc2 = grades.Cc2;
        module.LabGrade = module.HasLabExam ? grades.Lab : module.LabGrade; // keep a stored lab grade if lab is toggled off
        module.ExamGrade = grades.Exam;
        module.UpdatedAtUtc = Now;
        await TouchSemesterAsync(db, module.SemesterId, userId, ct);
        await db.SaveChangesAsync(ct);
    }

    public async Task SetModuleFormulaAsync(Guid moduleId, Guid? formulaId, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var module = await db.Modules.FirstOrDefaultAsync(m => m.Id == moduleId && m.UserId == userId, ct)
                     ?? throw new NotFoundException("That module");
        module.FormulaId = await ResolveFormulaAsync(db, userId, formulaId, module.HasLabExam, ct);
        module.UpdatedAtUtc = Now;
        await TouchSemesterAsync(db, module.SemesterId, userId, ct);
        await db.SaveChangesAsync(ct);
    }

    public async Task DeleteModuleAsync(Guid moduleId, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var module = await db.Modules.FirstOrDefaultAsync(m => m.Id == moduleId && m.UserId == userId, ct)
                     ?? throw new NotFoundException("That module");
        db.Modules.Remove(module);
        await TouchSemesterAsync(db, module.SemesterId, userId, ct);
        await db.SaveChangesAsync(ct);
    }

    public async Task MoveModuleAsync(Guid moduleId, int direction, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var module = await db.Modules.AsNoTracking().FirstOrDefaultAsync(m => m.Id == moduleId && m.UserId == userId, ct)
                     ?? throw new NotFoundException("That module");
        var list = await db.Modules.Where(m => m.SemesterId == module.SemesterId && m.UserId == userId)
            .OrderBy(m => m.SortOrder).ThenBy(m => m.Name).ToListAsync(ct);
        if (!Reorder(list, m => m.Id == moduleId, direction, (m, i) => m.SortOrder = i)) return;
        await db.SaveChangesAsync(ct);
    }

    // ---------- helpers ----------

    private static void Apply(Module m, ModuleForm f)
    {
        m.Name = f.Name.Trim();
        m.Code = Clean(f.Code);
        m.Coefficient = f.Coefficient;
        m.MaxGrade = f.MaxGrade;
        m.PassingThreshold = f.PassingThreshold;
        m.HasLabExam = f.HasLabExam;
    }

    private static string? Clean(string? s) => string.IsNullOrWhiteSpace(s) ? null : s.Trim();

    private static async Task TouchSemesterAsync(AppDbContext db, Guid semesterId, string userId, CancellationToken ct)
    {
        var s = await db.Semesters.FirstOrDefaultAsync(x => x.Id == semesterId && x.UserId == userId, ct);
        if (s is not null) s.UpdatedAtUtc = DateTime.UtcNow;
    }

    /// <summary>Picks a formula that belongs to the user and matches the module kind; falls back to the user's default.</summary>
    private static async Task<Guid?> ResolveFormulaAsync(AppDbContext db, string userId, Guid? requested, bool hasLab, CancellationToken ct)
    {
        if (requested is { } id)
        {
            var f = await db.Formulas.AsNoTracking().FirstOrDefaultAsync(x => x.Id == id && x.UserId == userId, ct)
                    ?? throw new UserFacingException("That formula isn't available.");
            if (f.ForLabExam != hasLab)
                throw new UserFacingException(hasLab
                    ? "Choose a formula made for modules with a lab exam."
                    : "Choose a formula made for modules without a lab exam.");
            return f.Id;
        }
        var candidates = await db.Formulas.AsNoTracking().Where(x => x.UserId == userId && x.ForLabExam == hasLab)
            .OrderByDescending(x => x.IsDefault).ThenBy(x => x.Name).Select(x => (Guid?)x.Id).FirstOrDefaultAsync(ct);
        return candidates;
    }

    private static bool Reorder<T>(List<T> list, Func<T, bool> isTarget, int direction, Action<T, int> setOrder)
    {
        var index = list.FindIndex(x => isTarget(x));
        if (index < 0) throw new NotFoundException();
        var target = index + Math.Sign(direction);
        if (direction == 0 || target < 0 || target >= list.Count) return false;
        (list[index], list[target]) = (list[target], list[index]);
        for (var i = 0; i < list.Count; i++) setOrder(list[i], i);
        return true;
    }

    internal static void ValidateSemester(SemesterForm f)
    {
        if (string.IsNullOrWhiteSpace(f.Name)) throw new UserFacingException("Give the semester a name.");
        if (f.Name.Trim().Length > 100) throw new UserFacingException("The semester name is too long (max 100).");
        if (f.AcademicYear?.Length > 50) throw new UserFacingException("The academic year is too long (max 50).");
        if (f.StartDate is { } s && f.EndDate is { } e && e < s)
            throw new UserFacingException("The end date can't be before the start date.");
    }

    internal static void ValidateModule(ModuleForm f)
    {
        if (string.IsNullOrWhiteSpace(f.Name)) throw new UserFacingException("Give the module a name.");
        if (f.Name.Trim().Length > 100) throw new UserFacingException("The module name is too long (max 100).");
        if (f.Code?.Length > 30) throw new UserFacingException("The module code is too long (max 30).");
        if (!double.IsFinite(f.Coefficient) || f.Coefficient < 0 || f.Coefficient > 1000)
            throw new UserFacingException("The coefficient must be between 0 and 1000.");
        if (!double.IsFinite(f.MaxGrade) || f.MaxGrade <= 0 || f.MaxGrade > 1000)
            throw new UserFacingException("The maximum grade must be greater than 0 (and at most 1000).");
        if (!double.IsFinite(f.PassingThreshold) || f.PassingThreshold < 0 || f.PassingThreshold > f.MaxGrade)
            throw new UserFacingException("The passing threshold must be between 0 and the maximum grade.");
    }

    internal static void ValidateGradesWithin(double? cc1, double? cc2, double? lab, double? exam, double max)
    {
        Check("CC1", cc1); Check("CC2", cc2); Check("Lab", lab); Check("Exam", exam);

        void Check(string label, double? v)
        {
            if (v is null) return;
            if (!double.IsFinite(v.Value) || v < 0 || v > max)
                throw new UserFacingException($"{label} must be between 0 and {GradeFormat.Number(max)}.");
        }
    }
}
