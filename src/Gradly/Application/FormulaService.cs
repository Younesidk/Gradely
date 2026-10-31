using System.ComponentModel.DataAnnotations;
using Gradly.Domain.Entities;
using Gradly.Domain.Formulas;
using Gradly.Domain.Grading;
using Gradly.Infrastructure.Data;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Application;

public sealed class FormulaForm
{
    [Required, StringLength(80)]
    public string Name { get; set; } = "";

    [Required, StringLength(Formula.MaxLength)]
    public string Expression { get; set; } = "";

    public bool ForLabExam { get; set; }
    public bool IsDefault { get; set; }
}

public sealed record FormulaView(GradeFormula Formula, int ModulesUsing);

/// <summary>CRUD for the signed-in user's grading formulas. Every query is scoped to the current user.</summary>
public sealed class FormulaService(IDbContextFactory<AppDbContext> dbFactory, ICurrentUser currentUser, TimeProvider clock)
{
    public const string DefaultNoLabName = "Standard (no lab)";
    public const string DefaultLabName = "Standard (with lab)";

    public static string? ValidateExpression(string? expression, bool forLabExam)
    {
        var parsed = Formula.Parse(expression, allowLab: forLabExam);
        if (!parsed.Success) return parsed.FirstError;
        if (parsed.Formula!.Variables.Count == 0) return "A formula must use at least one variable (CC1, CC2, Lab or Exam).";
        return null;
    }

    public async Task<List<FormulaView>> ListAsync(CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var formulas = await db.Formulas.AsNoTracking().Where(f => f.UserId == userId)
            .OrderBy(f => f.ForLabExam).ThenBy(f => f.Name).ToListAsync(ct);
        var usage = await db.Modules.Where(m => m.UserId == userId && m.FormulaId != null)
            .GroupBy(m => m.FormulaId!.Value).Select(g => new { g.Key, Count = g.Count() })
            .ToDictionaryAsync(x => x.Key, x => x.Count, ct);
        return formulas.Select(f => new FormulaView(f, usage.GetValueOrDefault(f.Id))).ToList();
    }

    public async Task<GradeFormula> SaveAsync(Guid? id, FormulaForm form, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        var name = (form.Name ?? "").Trim();
        if (name.Length is 0 or > 80) throw new UserFacingException("Give the formula a name (up to 80 characters).");
        var error = ValidateExpression(form.Expression, form.ForLabExam);
        if (error is not null) throw new UserFacingException(error);

        await using var db = await dbFactory.CreateDbContextAsync(ct);
        await using var tx = await db.Database.BeginTransactionAsync(ct);

        var clash = await db.Formulas.AnyAsync(f => f.UserId == userId && f.Id != (id ?? Guid.Empty)
                                                    && f.Name.ToLower() == name.ToLower(), ct);
        if (clash) throw new UserFacingException("You already have a formula with that name.");

        GradeFormula formula;
        if (id is null)
        {
            formula = new GradeFormula { UserId = userId };
            db.Formulas.Add(formula);
        }
        else
        {
            formula = await db.Formulas.FirstOrDefaultAsync(f => f.Id == id && f.UserId == userId, ct)
                      ?? throw new NotFoundException("That formula");
            if (formula.ForLabExam != form.ForLabExam &&
                await db.Modules.AnyAsync(m => m.FormulaId == formula.Id && m.UserId == userId, ct))
                throw new UserFacingException("This formula is used by modules, so its lab/no-lab type can't change. Reassign those modules first.");
        }

        formula.Name = name;
        formula.Expression = form.Expression.Trim();
        formula.ForLabExam = form.ForLabExam;
        formula.UpdatedAtUtc = clock.GetUtcNow().UtcDateTime;

        var hasDefaultOfKind = await db.Formulas.AnyAsync(f => f.UserId == userId && f.ForLabExam == form.ForLabExam
                                                              && f.IsDefault && f.Id != formula.Id, ct);
        if (form.IsDefault || !hasDefaultOfKind)
        {
            await ClearDefaultAsync(db, userId, form.ForLabExam, formula.Id, ct);
            formula.IsDefault = true;
        }
        else
        {
            formula.IsDefault = false;
        }

        await db.SaveChangesAsync(ct);
        await tx.CommitAsync(ct);
        return formula;
    }

    public async Task SetDefaultAsync(Guid id, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var formula = await db.Formulas.FirstOrDefaultAsync(f => f.Id == id && f.UserId == userId, ct)
                      ?? throw new NotFoundException("That formula");
        await ClearDefaultAsync(db, userId, formula.ForLabExam, formula.Id, ct);
        formula.IsDefault = true;
        await db.SaveChangesAsync(ct);
    }

    public async Task DeleteAsync(Guid id, CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var formula = await db.Formulas.FirstOrDefaultAsync(f => f.Id == id && f.UserId == userId, ct)
                      ?? throw new NotFoundException("That formula");
        var using_ = await db.Modules.CountAsync(m => m.FormulaId == id && m.UserId == userId, ct);
        if (using_ > 0)
            throw new UserFacingException($"This formula is used by {using_} module{(using_ == 1 ? "" : "s")}. Reassign them to another formula before deleting it.");
        db.Formulas.Remove(formula);
        await db.SaveChangesAsync(ct);
    }

    /// <summary>Adds the platform's default lab formula (set by the administrator) to the current user.</summary>
    public async Task<GradeFormula> AddPlatformLabFormulaAsync(CancellationToken ct = default)
    {
        var userId = await currentUser.GetUserIdAsync();
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        var expression = await GetPlatformLabFormulaAsync(db, ct)
                         ?? throw new UserFacingException("The administrator hasn't configured a default lab formula yet.");
        if (await db.Formulas.AnyAsync(f => f.UserId == userId && f.Name == DefaultLabName, ct))
            throw new UserFacingException("You already have the platform's lab formula.");
        var hasDefault = await db.Formulas.AnyAsync(f => f.UserId == userId && f.ForLabExam && f.IsDefault, ct);
        var f = new GradeFormula
        {
            UserId = userId, Name = DefaultLabName, Expression = expression, ForLabExam = true,
            IsDefault = !hasDefault, UpdatedAtUtc = clock.GetUtcNow().UtcDateTime,
        };
        db.Formulas.Add(f);
        await db.SaveChangesAsync(ct);
        return f;
    }

    public async Task<string?> GetPlatformLabFormulaAsync(CancellationToken ct = default)
    {
        await using var db = await dbFactory.CreateDbContextAsync(ct);
        return await GetPlatformLabFormulaAsync(db, ct);
    }

    internal static async Task<string?> GetPlatformLabFormulaAsync(AppDbContext db, CancellationToken ct)
    {
        var value = await db.Settings.AsNoTracking().Where(s => s.Key == SettingKeys.DefaultLabFormula)
            .Select(s => s.Value).FirstOrDefaultAsync(ct);
        return string.IsNullOrWhiteSpace(value) ? null : value;
    }

    /// <summary>Gives a brand-new account its starter formulas. Called by the provisioner, not by UI code.</summary>
    internal static async Task SeedDefaultsAsync(AppDbContext db, string userId, DateTime nowUtc, CancellationToken ct)
    {
        db.Formulas.Add(new GradeFormula
        {
            UserId = userId, Name = DefaultNoLabName, Expression = GradeCalculator.DefaultNoLabFormula,
            ForLabExam = false, IsDefault = true, UpdatedAtUtc = nowUtc,
        });
        if (await GetPlatformLabFormulaAsync(db, ct) is { } lab)
        {
            db.Formulas.Add(new GradeFormula
            {
                UserId = userId, Name = DefaultLabName, Expression = lab,
                ForLabExam = true, IsDefault = true, UpdatedAtUtc = nowUtc,
            });
        }
        await db.SaveChangesAsync(ct);
    }

    private static async Task ClearDefaultAsync(AppDbContext db, string userId, bool forLab, Guid exceptId, CancellationToken ct)
    {
        var others = await db.Formulas.Where(f => f.UserId == userId && f.ForLabExam == forLab && f.IsDefault && f.Id != exceptId)
            .ToListAsync(ct);
        foreach (var o in others) o.IsDefault = false;
    }
}
