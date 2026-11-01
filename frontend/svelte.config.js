export default {
  compilerOptions: {
    // Dialogs mount fresh for every open, so reading props once for the initial form values is intended.
    warningFilter: (w) => !['state_referenced_locally', 'a11y_interactive_supports_focus'].includes(w.code),
  },
};
