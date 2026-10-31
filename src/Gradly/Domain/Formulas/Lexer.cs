using System.Globalization;

namespace Gradly.Domain.Formulas;

internal enum TokenKind { Number, Identifier, Plus, Minus, Star, Slash, LParen, RParen, Comma, End }

internal readonly record struct Token(TokenKind Kind, string Text, int Position, double Number = 0);

/// <summary>Turns formula text into tokens. Pure string processing — nothing is ever executed.</summary>
internal static class Lexer
{
    public static bool TryTokenize(string text, out List<Token> tokens, out FormulaError? error)
    {
        tokens = new List<Token>();
        error = null;
        var i = 0;
        while (i < text.Length)
        {
            var c = text[i];
            if (char.IsWhiteSpace(c)) { i++; continue; }

            if (c is >= '0' and <= '9' or '.')
            {
                var start = i;
                var sawDot = false;
                while (i < text.Length && (text[i] is >= '0' and <= '9' || (text[i] == '.' && !sawDot)))
                {
                    if (text[i] == '.') sawDot = true;
                    i++;
                }
                var slice = text[start..i];
                if (slice == "." || !double.TryParse(slice, NumberStyles.AllowDecimalPoint, CultureInfo.InvariantCulture, out var n)
                    || double.IsInfinity(n))
                {
                    error = new FormulaError($"'{slice}' is not a valid number.", start);
                    return false;
                }
                // A number directly followed by a letter ("2CC1") is almost certainly a missing operator.
                if (i < text.Length && (IsAsciiLetter(text[i]) || text[i] == '_'))
                {
                    error = new FormulaError("Missing operator: write 2 * CC1 instead of 2CC1.", i);
                    return false;
                }
                tokens.Add(new Token(TokenKind.Number, slice, start, n));
                continue;
            }

            if (IsAsciiLetter(c) || c == '_')
            {
                var start = i;
                while (i < text.Length && (IsAsciiLetter(text[i]) || text[i] == '_' || (text[i] is >= '0' and <= '9'))) i++;
                tokens.Add(new Token(TokenKind.Identifier, text[start..i], start));
                continue;
            }

            var kind = c switch
            {
                '+' => TokenKind.Plus,
                '-' => TokenKind.Minus,
                '*' => TokenKind.Star,
                '/' => TokenKind.Slash,
                '(' => TokenKind.LParen,
                ')' => TokenKind.RParen,
                ',' => TokenKind.Comma,
                _ => (TokenKind?)null,
            };
            if (kind is null)
            {
                error = new FormulaError($"Unexpected character '{c}'.", i);
                return false;
            }
            tokens.Add(new Token(kind.Value, c.ToString(), i));
            i++;
        }
        tokens.Add(new Token(TokenKind.End, "", text.Length));
        return true;
    }

    private static bool IsAsciiLetter(char c) => c is >= 'a' and <= 'z' or >= 'A' and <= 'Z';
}
