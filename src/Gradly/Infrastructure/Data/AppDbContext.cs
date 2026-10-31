using Gradly.Domain.Entities;
using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Infrastructure.Data;

public class AppDbContext(DbContextOptions<AppDbContext> options) : IdentityDbContext<ApplicationUser>(options)
{
    public DbSet<Semester> Semesters => Set<Semester>();
    public DbSet<Module> Modules => Set<Module>();
    public DbSet<GradeFormula> Formulas => Set<GradeFormula>();
    public DbSet<Invitation> Invitations => Set<Invitation>();
    public DbSet<SystemSetting> Settings => Set<SystemSetting>();

    protected override void OnModelCreating(ModelBuilder b)
    {
        base.OnModelCreating(b);

        b.Entity<Semester>(e =>
        {
            e.HasKey(s => s.Id);
            e.Property(s => s.Name).HasMaxLength(100).IsRequired();
            e.Property(s => s.AcademicYear).HasMaxLength(50);
            e.HasOne(s => s.User).WithMany(u => u.Semesters).HasForeignKey(s => s.UserId).OnDelete(DeleteBehavior.Cascade);
            e.HasIndex(s => new { s.UserId, s.SortOrder });
        });

        b.Entity<Module>(e =>
        {
            e.HasKey(m => m.Id);
            e.Property(m => m.Name).HasMaxLength(100).IsRequired();
            e.Property(m => m.Code).HasMaxLength(30);
            e.HasOne(m => m.Semester).WithMany(s => s.Modules).HasForeignKey(m => m.SemesterId).OnDelete(DeleteBehavior.Cascade);
            // Formulas can only be deleted once no module references them (also enforced in FormulaService).
            e.HasOne(m => m.Formula).WithMany().HasForeignKey(m => m.FormulaId).OnDelete(DeleteBehavior.Restrict);
            e.HasIndex(m => new { m.UserId, m.SemesterId, m.SortOrder });
            e.HasIndex(m => m.FormulaId);
        });

        b.Entity<GradeFormula>(e =>
        {
            e.HasKey(f => f.Id);
            e.Property(f => f.Name).HasMaxLength(80).IsRequired();
            e.Property(f => f.Expression).HasMaxLength(Domain.Formulas.Formula.MaxLength).IsRequired();
            e.HasOne(f => f.User).WithMany(u => u.Formulas).HasForeignKey(f => f.UserId).OnDelete(DeleteBehavior.Cascade);
            e.HasIndex(f => f.UserId);
        });

        b.Entity<Invitation>(e =>
        {
            e.HasKey(i => i.Id);
            e.Property(i => i.Email).HasMaxLength(256).IsRequired();
            e.Property(i => i.NormalizedEmail).HasMaxLength(256).IsRequired();
            e.Property(i => i.TokenHash).HasMaxLength(64).IsRequired();
            e.HasIndex(i => i.TokenHash).IsUnique();
            e.HasIndex(i => i.NormalizedEmail);
        });

        b.Entity<SystemSetting>(e =>
        {
            e.HasKey(s => s.Key);
            e.Property(s => s.Key).HasMaxLength(100);
        });
    }
}
