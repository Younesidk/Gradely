namespace Gradly.Components.Shared;

public enum ToastKind { Success, Error, Info }

public sealed record ToastMessage(Guid Id, ToastKind Kind, string Text);

/// <summary>Per-circuit toast queue. <see cref="ToastHost"/> renders it.</summary>
public sealed class ToastService
{
    public event Action<ToastMessage>? OnShow;

    public void Success(string text) => Show(ToastKind.Success, text);
    public void Error(string text) => Show(ToastKind.Error, text);
    public void Info(string text) => Show(ToastKind.Info, text);

    private void Show(ToastKind kind, string text) => OnShow?.Invoke(new ToastMessage(Guid.NewGuid(), kind, text));
}

public sealed record ConfirmRequest(string Title, string Message, string ConfirmText, bool Danger, TaskCompletionSource<bool> Result);

/// <summary>Awaitable confirmation dialogs for destructive actions.</summary>
public sealed class ConfirmService
{
    public event Action<ConfirmRequest>? OnRequest;

    public Task<bool> ConfirmAsync(string title, string message, string confirmText = "Delete", bool danger = true)
    {
        var tcs = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        if (OnRequest is null) return Task.FromResult(false); // no host mounted: fail safe, do not proceed
        OnRequest(new ConfirmRequest(title, message, confirmText, danger, tcs));
        return tcs.Task;
    }
}
