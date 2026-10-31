using Microsoft.AspNetCore.Identity;

namespace Gradly.Domain.Entities;

public static class Roles
{
    public const string Admin = "Admin";
}

public class ApplicationUser : IdentityUser
{
    public DateTime CreatedAtUtc { get; set; } = DateTime.UtcNow;

    /// <summary>Disabled accounts cannot sign in; their data is preserved.</summary>
    public bool IsDisabled { get; set; }

    public DateTime? DisabledAtUtc { get; set; }
    public DateTime? LastLoginAtUtc { get; set; }

    public List<Semester> Semesters { get; set; } = [];
    public List<GradeFormula> Formulas { get; set; } = [];
}

public class Semester
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public string UserId { get; set; } = "";
    public ApplicationUser? User { get; set; }

    public string Name { get; set; } = "";
    public string? AcademicYear { get; set; }
    public DateOnly? StartDate { get; set; }
    public DateOnly? EndDate { get; set; }
    public int SortOrder { get; set; }
    public DateTime CreatedAtUtc { get; set; } = DateTime.UtcNow;
    public DateTime UpdatedAtUtc { get; set; } = DateTime.UtcNow;

    public List<Module> Modules { get; set; } = [];
}

public class Module
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid SemesterId { get; set; }
    public Semester? Semester { get; set; }

    /// <summary>Denormalised owner id so every module query can be filtered by owner directly.</summary>
    public string UserId { get; set; } = "";

    public string Name { get; set; } = "";
    public string? Code { get; set; }
    public double Coefficient { get; set; } = 1;
    public double MaxGrade { get; set; } = 20;
    public double PassingThreshold { get; set; } = 10;
    public bool HasLabExam { get; set; }

    // Null = not entered yet. 0 = an actual zero.
    public double? Cc1 { get; set; }
    public double? Cc2 { get; set; }
    public double? LabGrade { get; set; }
    public double? ExamGrade { get; set; }

    public Guid? FormulaId { get; set; }
    public GradeFormula? Formula { get; set; }

    public int SortOrder { get; set; }
    public DateTime UpdatedAtUtc { get; set; } = DateTime.UtcNow;
}

public class GradeFormula
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public string UserId { get; set; } = "";
    public ApplicationUser? User { get; set; }

    public string Name { get; set; } = "";
    public string Expression { get; set; } = "";

    /// <summary>True when the formula is meant for modules that have a lab exam.</summary>
    public bool ForLabExam { get; set; }

    /// <summary>The formula pre-selected for new modules of this kind.</summary>
    public bool IsDefault { get; set; }

    public DateTime UpdatedAtUtc { get; set; } = DateTime.UtcNow;
}

public enum InvitationStatus { Pending, Redeemed, Expired, Revoked }

public class Invitation
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public string Email { get; set; } = "";
    public string NormalizedEmail { get; set; } = "";

    /// <summary>SHA-256 (hex) of the invitation token. The raw token is never stored.</summary>
    public string TokenHash { get; set; } = "";

    public DateTime CreatedAtUtc { get; set; } = DateTime.UtcNow;
    public DateTime ExpiresAtUtc { get; set; }
    public DateTime? RedeemedAtUtc { get; set; }
    public DateTime? RevokedAtUtc { get; set; }
    public DateTime? EmailSentAtUtc { get; set; }

    public string? CreatedByUserId { get; set; }
    public string? RedeemedByUserId { get; set; }

    public InvitationStatus GetStatus(DateTime nowUtc) =>
        RedeemedAtUtc is not null ? InvitationStatus.Redeemed
        : RevokedAtUtc is not null ? InvitationStatus.Revoked
        : ExpiresAtUtc <= nowUtc ? InvitationStatus.Expired
        : InvitationStatus.Pending;
}

/// <summary>Small key/value table for platform-wide settings.</summary>
public class SystemSetting
{
    public string Key { get; set; } = "";
    public string Value { get; set; } = "";
}

public static class SettingKeys
{
    public const string AdminBootstrapped = "AdminBootstrappedAtUtc";
    public const string DefaultLabFormula = "DefaultLabFormula";
}
