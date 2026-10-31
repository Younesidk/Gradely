#pragma warning disable EF1002 // raw SQL is built from compile-time constants only, no user input
using Gradly.Infrastructure.Data;
using Microsoft.EntityFrameworkCore;

namespace Gradly.Application;

public sealed record UserStorage(string UserId, long Bytes, int Semesters, int Modules, int Formulas);

public sealed record StorageReport(
    DateTime CalculatedAtUtc,
    IReadOnlyDictionary<string, UserStorage> PerUser,
    long TotalEstimatedBytes,
    long DatabaseFileBytes,
    long WalFileBytes)
{
    public IEnumerable<UserStorage> Largest(int n) => PerUser.Values.OrderByDescending(u => u.Bytes).Take(n);
    public long For(string userId) => PerUser.TryGetValue(userId, out var u) ? u.Bytes : 0;
}

/// <summary>
/// Approximate *logical* per-user storage. SQLite keeps everything in one file, so physical bytes cannot be
/// attributed to users exactly; this sums the size of the values in each user-owned row plus a fixed
/// per-row overhead. The result is cached (a handful of GROUP BY queries, recomputed at most every few minutes).
/// </summary>
public sealed class StorageEstimator(IDbContextFactory<AppDbContext> dbFactory, GradlyOptions options, TimeProvider clock)
{
    public static readonly TimeSpan CacheDuration = TimeSpan.FromMinutes(5);
    private const int RowOverhead = 16;  // record header + rowid + b-tree cell bookkeeping (rough)
    private const int GuidText = 36;     // GUIDs are stored as TEXT
    private const int Real = 8;

    private readonly SemaphoreSlim _gate = new(1, 1);
    private StorageReport? _cached;

    public async Task<StorageReport> GetAsync(bool forceRefresh = false, CancellationToken ct = default)
    {
        var cached = _cached;
        if (!forceRefresh && cached is not null && clock.GetUtcNow().UtcDateTime - cached.CalculatedAtUtc < CacheDuration)
            return cached;

        await _gate.WaitAsync(ct);
        try
        {
            cached = _cached;
            if (!forceRefresh && cached is not null && clock.GetUtcNow().UtcDateTime - cached.CalculatedAtUtc < CacheDuration)
                return cached;
            return _cached = await ComputeAsync(ct);
        }
        finally
        {
            _gate.Release();
        }
    }

    public void Invalidate() => _cached = null;

    private async Task<StorageReport> ComputeAsync(CancellationToken ct)
    {
        await using var db = await dbFactory.CreateDbContextAsync(ct);

        // length(CAST(x AS BLOB)) = byte length of a text value.
        var semesters = await db.Database.SqlQueryRaw<Agg>($"""
            SELECT UserId AS UserId, COUNT(*) AS Cnt,
                   SUM({RowOverhead} + 2 * {GuidText} + 4
                       + length(CAST(Name AS BLOB)) + IFNULL(length(CAST(AcademicYear AS BLOB)), 0)
                       + CASE WHEN StartDate IS NULL THEN 0 ELSE 10 END + CASE WHEN EndDate IS NULL THEN 0 ELSE 10 END
                       + 2 * 28) AS Bytes
            FROM Semesters GROUP BY UserId
            """).ToListAsync(ct);

        var modules = await db.Database.SqlQueryRaw<Agg>($"""
            SELECT UserId AS UserId, COUNT(*) AS Cnt,
                   SUM({RowOverhead} + 3 * {GuidText} + CASE WHEN FormulaId IS NULL THEN 0 ELSE {GuidText} END
                       + length(CAST(Name AS BLOB)) + IFNULL(length(CAST(Code AS BLOB)), 0)
                       + 7 * {Real} + 2 + 4 + 28) AS Bytes
            FROM Modules GROUP BY UserId
            """).ToListAsync(ct);

        var formulas = await db.Database.SqlQueryRaw<Agg>($"""
            SELECT UserId AS UserId, COUNT(*) AS Cnt,
                   SUM({RowOverhead} + 2 * {GuidText} + length(CAST(Name AS BLOB)) + length(CAST(Expression AS BLOB)) + 2 + 28) AS Bytes
            FROM Formulas GROUP BY UserId
            """).ToListAsync(ct);

        // The Identity record itself (name, email, password hash, stamps, claims, role links).
        var accounts = await db.Database.SqlQueryRaw<Agg>($"""
            SELECT u.Id AS UserId, 1 AS Cnt,
                   {RowOverhead} + {GuidText}
                   + IFNULL(length(CAST(u.UserName AS BLOB)), 0) * 2 + IFNULL(length(CAST(u.Email AS BLOB)), 0) * 2
                   + IFNULL(length(CAST(u.PasswordHash AS BLOB)), 0) + IFNULL(length(CAST(u.SecurityStamp AS BLOB)), 0)
                   + IFNULL(length(CAST(u.ConcurrencyStamp AS BLOB)), 0) + IFNULL(length(CAST(u.PhoneNumber AS BLOB)), 0)
                   + 3 * 28 + 12
                   + (SELECT COUNT(*) FROM AspNetUserRoles r WHERE r.UserId = u.Id) * (2 * {GuidText} + {RowOverhead})
                   + (SELECT IFNULL(SUM(length(CAST(c.ClaimType AS BLOB)) + length(CAST(c.ClaimValue AS BLOB)) + {RowOverhead}), 0)
                      FROM AspNetUserClaims c WHERE c.UserId = u.Id) AS Bytes
            FROM AspNetUsers u
            """).ToListAsync(ct);

        var map = new Dictionary<string, (long Bytes, int S, int M, int F)>();
        void Add(IEnumerable<Agg> rows, Func<(long Bytes, int S, int M, int F), Agg, (long, int, int, int)> merge)
        {
            foreach (var r in rows)
            {
                var cur = map.GetValueOrDefault(r.UserId);
                map[r.UserId] = merge(cur, r);
            }
        }
        Add(accounts, (c, r) => (c.Bytes + r.Bytes, c.S, c.M, c.F));
        Add(semesters, (c, r) => (c.Bytes + r.Bytes, c.S + (int)r.Cnt, c.M, c.F));
        Add(modules, (c, r) => (c.Bytes + r.Bytes, c.S, c.M + (int)r.Cnt, c.F));
        Add(formulas, (c, r) => (c.Bytes + r.Bytes, c.S, c.M, c.F + (int)r.Cnt));

        var perUser = map.ToDictionary(kv => kv.Key,
            kv => new UserStorage(kv.Key, kv.Value.Bytes, kv.Value.S, kv.Value.M, kv.Value.F));

        return new StorageReport(clock.GetUtcNow().UtcDateTime, perUser, perUser.Values.Sum(u => u.Bytes),
            FileSize(options.DatabasePath), FileSize(options.DatabasePath + "-wal"));
    }

    private static long FileSize(string path)
    {
        try { return File.Exists(path) ? new FileInfo(path).Length : 0; }
        catch (IOException) { return 0; }
    }

    // Unmapped projection type for raw SQL. Properties must match the column aliases above.
    private sealed class Agg
    {
        public string UserId { get; set; } = "";
        public long Cnt { get; set; }
        public long Bytes { get; set; }
    }
}
