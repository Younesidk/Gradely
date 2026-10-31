using System.Globalization;
using Gradly.Domain.Grading;

namespace Gradly.Components.Shared;

public static class UiFormat
{
    public static string Date(DateTime utc) => utc.ToString("MMM d, yyyy", CultureInfo.InvariantCulture);
    public static string Date(DateTime? utc) => utc is null ? "—" : Date(utc.Value);
    public static string Stamp(DateTime utc) => utc.ToString("MMM d, yyyy HH:mm", CultureInfo.InvariantCulture) + " UTC";
    public static string Date(DateOnly d) => d.ToString("MMM d, yyyy", CultureInfo.InvariantCulture);

    /// <summary>Grade input value: empty when not entered, invariant decimal otherwise.</summary>
    public static string Input(double? v) => v is null ? "" : v.Value.ToString("0.##", CultureInfo.InvariantCulture);

    public static bool TryParseGrade(string? text, out double? value)
    {
        value = null;
        if (string.IsNullOrWhiteSpace(text)) return true; // cleared = not entered
        if (double.TryParse(text.Trim(), NumberStyles.Float, CultureInfo.InvariantCulture, out var d) && double.IsFinite(d))
        {
            value = d;
            return true;
        }
        return false;
    }

    public static string Bytes(long bytes)
    {
        string[] units = ["B", "KB", "MB", "GB"];
        double v = bytes;
        var i = 0;
        while (v >= 1024 && i < units.Length - 1) { v /= 1024; i++; }
        return i == 0 ? $"{bytes} B" : $"{v.ToString("0.##", CultureInfo.InvariantCulture)} {units[i]}";
    }

    public static string Average(SemesterSummary s) =>
        s.Average is null ? "—" : GradeFormat.Fixed2(s.Average);
}
