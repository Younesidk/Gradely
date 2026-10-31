// Shared helpers for the Developer Integrations pages and the OAuth consent screen.
import { date, stamp } from './format.js';

// Plain-language names for MCP tool calls in the activity log.
const OPERATION_LABELS = {
  list_semesters: 'Listed semesters',
  get_semester: 'Read a semester',
  create_semester: 'Created a semester',
  update_semester: 'Updated a semester',
  delete_semester: 'Deleted a semester',
  list_modules: 'Listed modules',
  get_module: 'Read a module',
  create_module: 'Created a module',
  update_module: 'Updated a module',
  set_module_formula: 'Changed a module formula',
  delete_module: 'Deleted a module',
  list_grades: 'Read grades',
  get_grade: 'Read a grade',
  add_grade: 'Added a grade',
  update_grade: 'Updated a grade',
  delete_grade: 'Removed a grade',
  get_grades_by_module: 'Read all grades',
  list_formulas: 'Listed formulas',
  get_formula: 'Read a formula',
  validate_formula: 'Checked a formula',
  create_formula: 'Created a formula',
  update_formula: 'Updated a formula',
  set_default_formula: 'Changed the default formula',
  delete_formula: 'Deleted a formula',
  get_module_average: 'Calculated a module result',
  get_semester_average: 'Calculated a semester average',
  get_overall_statistics: 'Read overall statistics',
  get_missing_assessments: 'Checked missing grades',
  get_semester_progress: 'Checked semester progress',
  get_grade_statistics: 'Read grade statistics',
  rate_limited: 'Rate limit reached',
};

export function operationLabel(op) {
  return OPERATION_LABELS[op] ?? op;
}

const ERROR_LABELS = {
  permission_denied: 'Not permitted',
  validation: 'Rejected',
  not_found: 'Not found',
  duplicate: 'Already exists',
  conflict: 'Conflict',
  rate_limited: 'Rate limited',
  timeout: 'Timed out',
  internal: 'Server error',
};

export function errorLabel(code) {
  return ERROR_LABELS[code] ?? 'Failed';
}

/** "Just now", "5 min ago", "3 h ago", or the date. */
export function ago(value, now = Date.now()) {
  if (!value) return 'Never';
  const s = Math.max(0, Math.round((now - new Date(value).getTime()) / 1000));
  if (s < 60) return 'Just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return date(value);
}

export { stamp };

/** Groups the scope catalog by kind of data, read before write, in catalog order. */
export function groupScopes(catalog) {
  const groups = [];
  for (const s of catalog) {
    let g = groups.find((x) => x.group === s.group);
    if (!g) {
      g = { group: s.group, read: null, write: null };
      groups.push(g);
    }
    g[s.access] = s;
  }
  return groups;
}

export const GROUP_LABELS = {
  semesters: 'Semesters',
  modules: 'Modules',
  grades: 'Grades',
  formulas: 'Formulas',
  statistics: 'Averages & statistics',
};

/** A short human summary of a scope list ("Full access", "Read-only", "3 permissions"). */
export function scopeSummary(scopes, catalog) {
  if (!catalog?.length) return `${scopes.length} permissions`;
  const all = catalog.map((s) => s.scope);
  const reads = catalog.filter((s) => s.access === 'read').map((s) => s.scope);
  if (all.every((s) => scopes.includes(s))) return 'Full access';
  if (scopes.length === reads.length && reads.every((s) => scopes.includes(s))) return 'Read-only';
  if (scopes.length === 1) return catalog.find((s) => s.scope === scopes[0])?.label ?? scopes[0];
  return `${scopes.length} permissions`;
}

export function hasWrite(scopes) {
  return scopes.some((s) => s.endsWith(':write'));
}

/** Expiry choices for new keys, as days from today (null = never). */
export const EXPIRY_CHOICES = [
  { value: '', label: 'Never' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '1 year' },
  { value: 'custom', label: 'Custom date' },
];

/** YYYY-MM-DD for today + days (UTC), the format the API expects. */
export function isoDateIn(days, from = new Date()) {
  const d = new Date(Date.UTC(from.getUTCFullYear(), from.getUTCMonth(), from.getUTCDate() + Number(days)));
  return d.toISOString().slice(0, 10);
}

export function windowText(seconds) {
  if (seconds % 60 === 0) {
    const m = seconds / 60;
    return m === 1 ? 'minute' : `${m} minutes`;
  }
  return `${seconds} seconds`;
}
