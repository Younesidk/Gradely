using Gradly.Domain.Entities;
using Gradly.Infrastructure.Data;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Application;

public sealed record Page<T>(IReadOnlyList<T> Items, int PageNumber, int PageSize, int Total)
{
    public int TotalPages => Math.Max(1, (int)Math.Ceiling(Total / (double)PageSize));
}

public sealed record AdminUserRow(
    string Id, string UserName, string Email, DateTime CreatedAtUtc, bool IsDisabled, bool IsAdmin,
    int Semesters, int Modules, long StorageBytes);

public sealed record AdminUserDetail(
    AdminUserRow Row, DateTime? LastLoginAtUtc, DateTime? DisabledAtUtc, int Formulas,
    int PendingInvitationsForEmail, IReadOnlyList<SemesterView> Semesters);

public sealed record AdminOverview(
    int Users, int ActiveUsers, int DisabledUsers, int PendingInvitations, int Semesters, int Modules,
    StorageReport Storage, IReadOnlyList<AdminUserRow> LargestUsers, string? DefaultLabFormula);

/// <summary>Administrator operations. Every public method re-checks the admin role on the server.</summary>
public sealed class AdminService(
    IDbContextFactory<AppDbContext> dbFactory,
    IServiceScopeFactory scopeFactory,
    ICurrentUser currentUser,
    StorageEstimator storage,
    TimeProvider clock)
{
    public async Task<AdminOverview> GetOverviewAsync(bool refreshStorage = false, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var now = clock.GetUtcNow().UtcDateTime;
        var report = await storage.GetAsync(refreshStorage, ct);

        var users = await db.Users.CountAsync(ct);
        var disabled = await db.Users.CountAsync(u => u.IsDisabled, ct);
        var pending = await db.Invitations.CountAsync(i => i.RedeemedAtUtc == null && i.RevokedAtUtc == null && i.ExpiresAtUtc > now, ct);
        var semesters = await db.Semesters.CountAsync(ct);
        var modules = await db.Modules.CountAsync(ct);

        var topIds = report.Largest(5).Select(u => u.UserId).ToList();
        var top = (await RowsAsync(db, db.Users.Where(u => topIds.Contains(u.Id)), report, ct))
            .OrderByDescending(r => r.StorageBytes).ToList();

        return new AdminOverview(users, users - disabled, disabled, pending, semesters, modules, report, top,
            await FormulaService.GetPlatformLabFormulaAsync(db, ct));
    }

    public async Task<Page<AdminUserRow>> GetUsersAsync(string? search, int page, int pageSize = 10, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        pageSize = Math.Clamp(pageSize, 5, 100);
        await using var db = await dbFactory.CreateDbContextAsync(ct);

        var query = db.Users.AsNoTracking();
        if (!string.IsNullOrWhiteSpace(search))
        {
            var like = $"%{EscapeLike(search.Trim())}%";
            query = query.Where(u => EF.Functions.Like(u.UserName!, like, "\\") || EF.Functions.Like(u.Email!, like, "\\"));
        }

        var total = await query.CountAsync(ct);
        var pages = Math.Max(1, (int)Math.Ceiling(total / (double)pageSize));
        page = Math.Clamp(page, 1, pages);
        var pageUsers = query.OrderBy(u => u.UserName).Skip((page - 1) * pageSize).Take(pageSize);
        var report = await storage.GetAsync(ct: ct);
        var rows = await RowsAsync(db, pageUsers, report, ct);
        return new Page<AdminUserRow>(rows, page, pageSize, total);
    }

    public async Task<AdminUserDetail> GetUserAsync(string userId, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var report = await storage.GetAsync(ct: ct);
        var user = await db.Users.AsNoTracking().FirstOrDefaultAsync(u => u.Id == userId, ct)
                   ?? throw new NotFoundException("That user");
        var row = (await RowsAsync(db, db.Users.Where(u => u.Id == userId), report, ct)).First();
        var formulas = await db.Formulas.CountAsync(f => f.UserId == userId, ct);
        var now = clock.GetUtcNow().UtcDateTime;
        var pending = await db.Invitations.CountAsync(i => i.NormalizedEmail == user.NormalizedEmail
            && i.RedeemedAtUtc == null && i.RevokedAtUtc == null && i.ExpiresAtUtc > now, ct);
        // Read-only inspection of another user's academic data (administrators only).
        var semesters = await AcademicReader.LoadAsync(db, userId, null, ct);
        return new AdminUserDetail(row, user.LastLoginAtUtc, user.DisabledAtUtc, formulas, pending, semesters);
    }

    public async Task<(AdminUserRow User, SemesterView Semester)> GetUserSemesterAsync(string userId, Guid semesterId, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var report = await storage.GetAsync(ct: ct);
        var row = (await RowsAsync(db, db.Users.Where(u => u.Id == userId), report, ct)).FirstOrDefault()
                  ?? throw new NotFoundException("That user");
        var semester = (await AcademicReader.LoadAsync(db, userId, semesterId, ct)).FirstOrDefault()
                       ?? throw new NotFoundException("That semester");
        return (row, semester);
    }

    public async Task SetDisabledAsync(string userId, bool disabled, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        var adminId = await currentUser.GetUserIdAsync();
        if (disabled && userId == adminId)
            throw new UserFacingException("You can't disable your own account.");

        await using var scope = scopeFactory.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = await users.FindByIdAsync(userId) ?? throw new NotFoundException("That user");
        if (disabled && await users.IsInRoleAsync(user, Roles.Admin))
            throw new UserFacingException("Administrator accounts can't be disabled.");
        if (user.IsDisabled == disabled) return;

        user.IsDisabled = disabled;
        user.DisabledAtUtc = disabled ? clock.GetUtcNow().UtcDateTime : null;
        var result = await users.UpdateAsync(user);
        if (!result.Succeeded) throw new UserFacingException("The account couldn't be updated.");
        // Rotating the stamp invalidates every existing cookie and live circuit for this user.
        await users.UpdateSecurityStampAsync(user);
    }

    public async Task SetDefaultLabFormulaAsync(string? expression, CancellationToken ct = default)
    {
        await Guard.RequireAdminAsync(currentUser);
        expression = expression?.Trim();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var setting = await db.Settings.FirstOrDefaultAsync(s => s.Key == SettingKeys.DefaultLabFormula, ct);

        if (string.IsNullOrEmpty(expression))
        {
            if (setting is not null) db.Settings.Remove(setting);
        }
        else
        {
            var error = FormulaService.ValidateExpression(expression, forLabExam: true);
            if (error is not null) throw new UserFacingException(error);
            if (setting is null) db.Settings.Add(new SystemSetting { Key = SettingKeys.DefaultLabFormula, Value = expression });
            else setting.Value = expression;
        }
        await db.SaveChangesAsync(ct);
    }

    private async Task<List<AdminUserRow>> RowsAsync(AppDbContext db, IQueryable<ApplicationUser> users, StorageReport report, CancellationToken ct)
    {
        var raw = await users.AsNoTracking().Select(u => new
        {
            u.Id, u.UserName, u.Email, u.CreatedAtUtc, u.IsDisabled,
            Semesters = db.Semesters.Count(s => s.UserId == u.Id),
            Modules = db.Modules.Count(m => m.UserId == u.Id),
        }).ToListAsync(ct);

        var ids = raw.Select(r => r.Id).ToList();
        var adminIds = await (from ur in db.UserRoles
                              join r in db.Roles on ur.RoleId equals r.Id
                              where r.Name == Roles.Admin && ids.Contains(ur.UserId)
                              select ur.UserId).ToListAsync(ct);

        return raw.Select(r => new AdminUserRow(r.Id, r.UserName ?? "", r.Email ?? "", r.CreatedAtUtc, r.IsDisabled,
            adminIds.Contains(r.Id), r.Semesters, r.Modules, report.For(r.Id))).ToList();
    }

    private static string EscapeLike(string s) => s.Replace("\\", "\\\\").Replace("%", "\\%").Replace("_", "\\_");
}
