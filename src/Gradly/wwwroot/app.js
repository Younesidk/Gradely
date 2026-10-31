// Tiny helpers used from Blazor via IJSRuntime. No frameworks.
window.gradly = {
    toggleTheme: function () {
        var next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
        document.documentElement.setAttribute('data-theme', next);
        try { localStorage.setItem('gradly-theme', next); } catch (e) { /* storage unavailable */ }
        return next;
    },

    // Inserts text at the caret of a textarea/input and returns the new value.
    insertAtCursor: function (id, text) {
        var el = document.getElementById(id);
        if (!el) return '';
        var start = el.selectionStart == null ? el.value.length : el.selectionStart;
        var end = el.selectionEnd == null ? el.value.length : el.selectionEnd;
        el.value = el.value.slice(0, start) + text + el.value.slice(end);
        var caret = start + text.length;
        el.focus();
        el.setSelectionRange(caret, caret);
        return el.value;
    },

    copyText: async function (text) {
        try {
            if (navigator.clipboard && window.isSecureContext) {
                await navigator.clipboard.writeText(text);
                return true;
            }
        } catch (e) { /* fall through */ }
        try {
            var ta = document.createElement('textarea');
            ta.value = text;
            ta.style.position = 'fixed';
            ta.style.opacity = '0';
            document.body.appendChild(ta);
            ta.select();
            var ok = document.execCommand('copy');
            document.body.removeChild(ta);
            return ok;
        } catch (e) { return false; }
    },

    selectElement: function (id) {
        var el = document.getElementById(id);
        if (el && el.select) el.select();
    }
};
