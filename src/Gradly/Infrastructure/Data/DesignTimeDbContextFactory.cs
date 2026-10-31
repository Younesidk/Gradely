using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

namespace Gradly.Infrastructure.Data;

/// <summary>
/// Used only by the EF Core command-line tools (<c>dotnet ef ...</c>) so they never need to start the web app.
/// Honors GRADLY_DATA_DIR; defaults to ./App_Data next to the project.
/// </summary>
public sealed class DesignTimeDbContextFactory : IDesignTimeDbContextFactory<AppDbContext>
{
    public AppDbContext CreateDbContext(string[] args)
    {
        var dir = Environment.GetEnvironmentVariable("GRADLY_DATA_DIR");
        dir = Path.GetFullPath(string.IsNullOrWhiteSpace(dir) ? Path.Combine(Directory.GetCurrentDirectory(), "App_Data") : dir);
        Directory.CreateDirectory(dir);
        var options = new DbContextOptionsBuilder<AppDbContext>()
            .UseSqlite($"Data Source={Path.Combine(dir, "gradly.db")};Foreign Keys=True")
            .Options;
        return new AppDbContext(options);
    }
}
