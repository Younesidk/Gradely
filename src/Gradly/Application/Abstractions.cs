using System.Security.Cryptography;
using System.Text;

namespace Gradly.Application;

/// <summary>Runtime configuration, populated from environment variables / appsettings.</summary>
public sealed class GradlyOptions
{
    public string DataDirectory { get; set; } = "";
    public string BaseUrl { get; set; } = "http://localhost:5103";
    public string? ResendApiKey { get; set; }
    public string? ResendFromEmail { get; set; }
    public int AuthRequestsPerMinute { get; set; } = 10;
    public int InvitationValidDays { get; set; } = 7;

    public bool EmailConfigured =>
        !string.IsNullOrWhiteSpace(ResendApiKey) && !string.IsNullOrWhiteSpace(ResendFromEmail);

    public string DatabasePath => Path.Combine(DataDirectory, "gradly.db");

    public static GradlyOptions From(IConfiguration c, string contentRoot)
    {
        var dir = c["GRADLY_DATA_DIR"];
        return new GradlyOptions
        {
            DataDirectory = Path.GetFullPath(string.IsNullOrWhiteSpace(dir) ? Path.Combine(contentRoot, "App_Data") : dir),
            BaseUrl = (c["APP_BASE_URL"] is { Length: > 0 } b ? b : "http://localhost:5103").TrimEnd('/'),
            ResendApiKey = c["RESEND_API_KEY"],
            ResendFromEmail = c["RESEND_FROM_EMAIL"],
            AuthRequestsPerMinute = int.TryParse(c["GRADLY_AUTH_RATE_LIMIT"], out var rl) && rl > 0 ? rl : 10,
            InvitationValidDays = 7,
        };
    }
}

/// <summary>Who is making the current request / circuit. Services never trust a user id passed by callers.</summary>
public interface ICurrentUser
{
    /// <summary>The signed-in user's id, or throws <see cref="UnauthorizedAccessException"/>.</summary>
    Task<string> GetUserIdAsync();

    Task<bool> IsAdminAsync();
}

/// <summary>A problem the user can fix (validation, business rule). Message is safe to show.</summary>
public class UserFacingException(string message) : Exception(message);

/// <summary>Raised when something does not exist <i>for the current user</i>. Never reveals other users' data.</summary>
public sealed class NotFoundException(string what = "That item") : UserFacingException($"{what} was not found.");

public static class Guard
{
    public static async Task RequireAdminAsync(ICurrentUser user)
    {
        if (!await user.IsAdminAsync())
            throw new UnauthorizedAccessException("Administrator access is required.");
    }
}

public static class TokenHasher
{
    /// <summary>Cryptographically secure URL-safe token (256 bits).</summary>
    public static string NewToken() => Base64Url(RandomNumberGenerator.GetBytes(32));

    public static string Hash(string token) =>
        Convert.ToHexStringLower(SHA256.HashData(Encoding.UTF8.GetBytes(token)));

    private static string Base64Url(byte[] bytes) =>
        Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');
}
