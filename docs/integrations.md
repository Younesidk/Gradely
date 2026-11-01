# Gradly developer integrations (MCP, OAuth, API keys)

Gradly includes a [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server. It lets AI assistants such as
Hermes or Claude read your academic data and, if you allow it, change it on your behalf: create semesters and
modules, enter grades, edit formulas and calculate averages. Everything is managed in **Account settings → Developer
integrations** (`/settings/integrations`).

- [1. What Gradly MCP is](#1-what-gradly-mcp-is)
- [2. Connecting Hermes](#2-connecting-hermes)
- [3. Connecting other MCP clients](#3-connecting-other-mcp-clients)
- [4. How OAuth sign-in works](#4-how-oauth-sign-in-works)
- [5. Creating an API key](#5-creating-an-api-key)
- [6. Authenticating with an API key](#6-authenticating-with-an-api-key)
- [7. Permissions](#7-permissions)
- [8. Rate limits](#8-rate-limits)
- [9. Available tools](#9-available-tools)
- [10. Example calls](#10-example-calls)
- [11. Errors](#11-errors)
- [12. Security recommendations](#12-security-recommendations)
- [13. Operator notes](#13-operator-notes)

## 1. What Gradly MCP is

| | |
| --- | --- |
| Endpoint | `https://<your-gradly-host>/mcp` (exactly `APP_BASE_URL` + `/mcp`) |
| Transport | MCP Streamable HTTP, stateless, JSON responses |
| Authentication | OAuth 2.1 (authorization code + PKCE) **or** a personal API key, as `Authorization: Bearer …` |
| Implementation | Built into the Gradly server with the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk); no extra process |

Every request is authenticated, rate limited and checked against the credential's permissions. Tools run as the
account that owns the credential: a client can never pick another user. They go through the same validation and
grading engine as the web app, so an assistant gets exactly the averages you see in Gradly. A missing grade is never
treated as zero; results say what is missing instead.

## 2. Connecting Hermes

[Hermes Agent](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp) reads MCP servers from
`~/.hermes/config.yaml`.

**With OAuth (recommended)**: no key to copy, and you choose the permissions in your browser:

```yaml
mcp_servers:
  gradly:
    url: "https://your-gradly-host/mcp"
    auth: oauth
```

Then run `hermes mcp login gradly` (or `/reload-mcp` in a running session). Hermes opens Gradly in your browser; sign
in, review the permissions, untick what you don't want and click **Allow access**. Hermes registers itself
automatically (dynamic client registration) and keeps its tokens in `~/.hermes/mcp-tokens/`. The connection appears
under **MCP connections → Connected applications**.

**With an API key** (create one first, see [section 5](#5-creating-an-api-key)):

```yaml
mcp_servers:
  gradly:
    url: "https://your-gradly-host/mcp"
    headers:
      Authorization: "Bearer gradly_sk_your_key_here"
```

Run `/reload-mcp` after editing the file. `config.yaml` then contains a secret: keep it private.

## 3. Connecting other MCP clients

Any client that supports remote MCP servers over Streamable HTTP works.

- **Clients with OAuth support** (Claude, Claude Code, MCP Inspector, Cursor, …): add the endpoint URL as a remote /
  custom connector. The client discovers everything else on its own.
- **Clients that take headers**: most accept a JSON configuration like this one:

  ```json
  {
    "mcpServers": {
      "gradly": {
        "type": "http",
        "url": "https://your-gradly-host/mcp",
        "headers": { "Authorization": "Bearer gradly_sk_your_key_here" }
      }
    }
  }
  ```

- **Your own code**: with the official Go SDK,

  ```go
  transport := &mcp.StreamableClientTransport{Endpoint: "https://your-gradly-host/mcp", HTTPClient: clientThatAddsBearerHeader}
  session, err := mcp.NewClient(&mcp.Implementation{Name: "my-script", Version: "1"}, nil).Connect(ctx, transport, nil)
  ```

The legacy HTTP+SSE transport is not offered (it was deprecated by the MCP specification).

## 4. How OAuth sign-in works

Gradly is its own OAuth 2.1 authorization server and uses your normal Gradly sign-in; there is no second account.

1. The client calls `/mcp` without a token and receives `401` with
   `WWW-Authenticate: Bearer resource_metadata="…/.well-known/oauth-protected-resource/mcp"`.
2. It reads the protected resource metadata (RFC 9728), then the authorization server metadata at
   `/.well-known/oauth-authorization-server` (RFC 8414).
3. It registers itself at `/oauth/register` (RFC 7591 dynamic client registration; public clients only).
4. It opens `/oauth/authorize` in your browser with a PKCE `S256` challenge. If you're signed out, Gradly asks you
   to sign in first and brings you back.
5. The consent screen shows the application's name, where it will send you back, and the permissions it asked for.
   You can untick any of them. You can never grant more than the application requested.
6. Gradly redirects to the application with a single-use code (valid 5 minutes), plus `state` and `iss`.
7. The application exchanges the code and its PKCE verifier at `/oauth/token` for an access token (valid 1 hour) and
   a refresh token (valid 30 days).
8. Refresh tokens rotate: each refresh returns a new pair, and the old refresh token stops working. If a used refresh
   token or code is ever presented again, Gradly assumes it was stolen and revokes the whole connection.

What is enforced: exact redirect URI matching (loopback addresses may change port, as RFC 8252 requires), PKCE `S256`
(no `plain`), audience binding to the `/mcp` resource (RFC 8707), and no implicit or password grants.
Applications can revoke their own tokens at `/oauth/revoke` (RFC 7009). You can disconnect any application from
the dashboard; disconnecting one doesn't affect the others or your API keys.

Supported redirect URIs: `https://…`, `http://` on `localhost` / `127.0.0.1` / `[::1]`, and private app schemes
(`cursor://…`, `com.example.app:/…`). Client ID metadata documents are not supported. Clients that need one must
use dynamic registration or an API key.

## 5. Creating an API key

1. Open **Account settings → Developer integrations → API keys** and click **Create key**.
2. Name it after what will use it (e.g. "Hermes", "Grade import script") and optionally describe it.
3. Pick an expiry (never, 30 days, 90 days, 1 year or a date). The key works until the end of that day (UTC).
4. Choose permissions: **Read-only** is selected by default.
5. Click **Create key** and copy the key. **It is shown only once.** Gradly stores only its SHA-256 fingerprint and
   can't show it again.

Keys look like `gradly_sk_` followed by 43 random characters (256 bits). The list shows only the first few
characters, so you can tell keys apart. You can have up to 25 active keys.

- **Edit**: rename the key, change its description or permissions. Changes apply to its next request.
- **Rotate**: issues a new secret for the same key (same name, permissions and history). The old secret stops
  working immediately; update whatever uses it.
- **Revoke**: stops the key immediately. This can't be undone.
- **Delete**: removes a revoked or expired key from the list. Its activity stays in the log.

## 6. Authenticating with an API key

Send the key as a bearer token on every request to the MCP endpoint:

```http
POST /mcp HTTP/1.1
Host: your-gradly-host
Authorization: Bearer gradly_sk_…
Content-Type: application/json
Accept: application/json, text/event-stream

{"jsonrpc":"2.0","id":1,"method":"tools/list"}
```

API keys and OAuth tokens only work on `/mcp`. They are not accepted by the web app's `/api` endpoints, and a
browser session cookie is not accepted by `/mcp`.

## 7. Permissions

Every key and connection carries its own scopes. Gradly checks them on the server on every tool call, and `tools/list`
only shows the tools a credential may call.

| Data | View | Change |
| --- | --- | --- |
| Semesters | `semesters:read`: names, academic years, dates | `semesters:write`: create, edit, delete (deleting removes the modules too) |
| Modules | `modules:read`: names, codes, coefficients, scales, formula used | `modules:write`: create, edit, delete, choose the formula |
| Grades | `grades:read`: CC1, CC2, lab and exam grades and module results | `grades:write`: add, change, remove grades |
| Formulas | `formulas:read`: formulas and whether they're valid | `formulas:write`: create, edit, delete, choose defaults |
| Statistics | `statistics:read`: averages, pass/fail counts, missing assessments, required exam grade | — |

"Change" does not include "View": a key with only `grades:write` can enter grades but can't read them. In the key
dialog, turning on "Change" also ticks "View" for the same data; you can untick it. Choose the narrowest set that
works, for example read-only for a study-planning assistant.

## 8. Rate limits

| Limit | Default | Variable |
| --- | ---: | --- |
| Per API key or OAuth connection | 100 requests | `GRADLY_API_KEY_RATE_LIMIT` |
| Per Gradly user, across all their keys and connections | 150 requests | `GRADLY_USER_RATE_LIMIT` |
| Window (rolling) | 5 minutes | `GRADLY_RATE_LIMIT_WINDOW` (Go duration, e.g. `5m`, `90s`) |

Every HTTP request to `/mcp` counts, including `initialize` and `tools/list`. The window is an exact sliding log, so
there's no burst at a window boundary. Both limits are checked and recorded together and atomically, so concurrent
requests can't squeeze past them. A rejected request consumes nothing and runs nothing.

Every accepted response carries `X-RateLimit-Limit`, `X-RateLimit-Remaining` and `X-RateLimit-Reset` (Unix time when
the next slot frees up), describing whichever limit is tighter. Over the limit the answer is:

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 37
Content-Type: application/json

{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"Rate limit exceeded: at most 100 requests per 5 minutes. Try again in 37 seconds."}}
```

Separately, more than 20 failed authentications per minute from one IP address are answered with `429` for a
minute. The dashboard's Overview shows how much of the user window you've used.

## 9. Available tools

All tools use exact, strict input schemas: unknown arguments are rejected, and enumerations such as `assessment` are
listed in the schema. They return structured content, and are annotated read-only, additive or destructive.

**Semesters**

| Tool | Scope | What it does |
| --- | --- | --- |
| `list_semesters` | semesters:read | All semesters, in order, with module counts |
| `get_semester` | semesters:read | One semester's details |
| `create_semester` | semesters:write | New semester (`name`, optional `academic_year`, `start_date`, `end_date`, `idempotency_key`) |
| `update_semester` | semesters:write | Partial update; `""` clears an optional field |
| `delete_semester` | semesters:write | Deletes it **and its modules**; requires `confirm: true` |

**Modules**

| Tool | Scope | What it does |
| --- | --- | --- |
| `list_modules` | modules:read | Module settings, all or one semester (`semester_id`) |
| `get_module` | modules:read | One module's settings |
| `create_module` | modules:write | New module; defaults: coefficient 1, max grade 20, pass mark 10, no lab, default formula |
| `update_module` | modules:write | Partial update; grades must still fit a new max grade |
| `set_module_formula` | modules:write | Choose the module's formula (lab/no-lab kind must match) |
| `delete_module` | modules:write | Deletes the module and its grades; requires `confirm: true` |

Moving a module to another semester isn't a Gradly feature, so there's no tool for it.

**Grades.** Assessments are exactly `CC1`, `CC2`, `Lab` (modules with a lab exam only) and `Exam`. There is one
grade per assessment per module.

| Tool | Scope | What it does |
| --- | --- | --- |
| `list_grades` | grades:read | A module's grades (`null` = not entered) |
| `get_grade` | grades:read | One assessment's grade |
| `add_grade` | grades:write | Enters a grade that isn't set yet; fails if it is (so a retry never overwrites) |
| `update_grade` | grades:write | Sets a grade, replacing any value; returns the previous value |
| `delete_grade` | grades:write | Clears a grade (it becomes missing, not zero) |
| `get_grades_by_module` | grades:read | Every grade grouped by semester and module (optionally one semester) |

Grade changes return the module's recalculated result.

**Formulas**

| Tool | Scope | What it does |
| --- | --- | --- |
| `list_formulas` | formulas:read | Formulas with kind, default flag, usage and variables |
| `get_formula` | formulas:read | One formula |
| `validate_formula` | formulas:read | Parses an expression without saving; returns errors with positions |
| `create_formula` | formulas:write | New formula (validated by the Gradly formula engine) |
| `update_formula` | formulas:write | Partial update; modules using it are recalculated immediately |
| `set_default_formula` | formulas:write | Default for new modules of its kind |
| `delete_formula` | formulas:write | Only when no module uses it; requires `confirm: true` |

**Statistics** (`statistics:read`)

| Tool | What it does |
| --- | --- |
| `get_module_average` | Module grade and pass/fail, or what's missing and the exam grade needed to pass |
| `get_semester_average` | Coefficient-weighted average, counts, every module's result; `is_provisional` when some modules are incomplete |
| `get_overall_statistics` | Average across all semesters plus each semester's summary |
| `get_missing_assessments` | Modules still missing grades their formula needs |
| `get_semester_progress` | Modules complete, assessments entered vs. required, coefficient covered |
| `get_grade_statistics` | Per assessment: entered/missing counts and mean/min/max as a % of each module's scale |

**Retries and idempotency.** `create_semester`, `create_module` and `create_formula` accept an optional
`idempotency_key` (1–100 characters, `A-Z a-z 0-9 . _ : -`). Repeating a call with the same key within 24 hours
returns the first result instead of creating a duplicate. Updates and deletes are naturally idempotent, and
`add_grade` refuses to overwrite.

## 10. Example calls

Create a module, then enter a grade:

```json
{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create_module",
 "arguments":{"semester_id":"64294d75-…","name":"Analysis","coefficient":2,"idempotency_key":"analysis-2026"}}}
```

```json
{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"add_grade",
 "arguments":{"module_id":"9b13a960-…","assessment":"CC2","value":15}}}
```

Structured result of `add_grade` (with the default formula `(CC1 + CC2) / 2 * 0.4 + Exam * 0.6` and CC1 = 11):

```json
{
  "module_id": "9b13a960-…", "module_name": "Analysis", "assessment": "CC2", "value": 15, "previous_value": null,
  "result": {
    "status": "incomplete", "status_text": "Incomplete — missing Exam", "grade": null, "passed": null,
    "max_grade": 20, "passing_threshold": 10, "coefficient": 2, "missing_assessments": ["Exam"],
    "required_exam": { "kind": "needed", "value": 8, "message": "You need at least 8/20 in your final exam to pass." },
    "formula_name": "Standard (no lab)", "formula_error": null
  }
}
```

Delete with confirmation (an assistant should ask you first):

```json
{"name":"delete_semester","arguments":{"semester_id":"64294d75-…","confirm":true}}
```

## 11. Errors

| Situation | What the client sees |
| --- | --- |
| No, invalid, expired or revoked token | HTTP `401` with `WWW-Authenticate: Bearer resource_metadata="…"` |
| Rate limit | HTTP `429`, `Retry-After`, JSON-RPC error body (see [section 8](#8-rate-limits)) |
| Tool not permitted | The tool isn't listed; calling it anyway fails (`unknown tool`) |
| Invalid arguments (missing, wrong type, unknown field, bad enum) | Tool error naming the problem, before anything runs |
| Business rule (grade above the scale, lab on a non-lab module, formula syntax, end before start, …) | Tool result with `isError: true` and Gradly's own message, e.g. `Exam must be between 0 and 20.` |
| Not found or belongs to someone else | `That module was not found.` (the same answer either way) |
| Unexpected server error | `An unexpected error occurred. Please try again.` The details are only in the server log |

OAuth endpoints answer with RFC 6749 errors (`invalid_grant`, `invalid_client`, `invalid_scope`, `invalid_target`, …).
Authorization requests with an unknown client or unregistered redirect URI are never redirected; the user sees the
error instead.

## 12. Security recommendations

- Prefer OAuth: no secret to copy, scoped per application, easy to disconnect.
- Give each tool its own key, and only the permissions it needs. Start read-only.
- Set an expiry on keys used by scripts and rotate long-lived keys periodically.
- Never commit keys or paste them in chats. Store them in a password manager or secret store.
- If a key leaks, revoke or rotate it at once: it stops working on the next request.
- Check **Activity** now and then for actions you don't recognise.
- Run Gradly behind HTTPS in production (`APP_BASE_URL=https://…`, `GRADLY_SECURE_COOKIES=true`). OAuth redirect and
  token traffic must not cross the internet in clear text.

How Gradly protects credentials:

- **Storage.** API keys, OAuth codes, access tokens and refresh tokens are stored only as SHA-256 hashes; the raw
  values exist only in the response that issues them. Because every secret carries 256 bits from the OS CSPRNG, a
  fast hash is sufficient, and the lookup itself reveals nothing.
- **Logging.** Secrets and authorization headers are never logged. The activity log keeps the operation name and
  outcome, never arguments or grades.
- **Revocation.** Revoking a key or connection, disabling an account or deleting a grant takes effect on the next
  request: nothing is cached.
- **Account isolation.** Every query is scoped to the credential's user. Ids belonging to others answer "not found".
- **CSRF.** The management API needs the signed-in session plus the `X-Gradly` CSRF header. `/mcp` and the OAuth
  endpoints use no cookies and allow cross-origin calls (CORS `*`): CORS is not what protects them, the bearer
  token / PKCE is.
- **Limits.** Request bodies on `/mcp` are capped at 1 MiB, tool calls time out after 15 seconds, and server
  read/write timeouts apply.

## 13. Operator notes

Configuration (all optional):

| Variable | Default | Purpose |
| --- | --- | --- |
| `APP_BASE_URL` | `http://localhost:8080` | **Must be the public URL.** It's the OAuth issuer and defines the MCP resource (`APP_BASE_URL/mcp`) tokens are bound to |
| `GRADLY_API_KEY_RATE_LIMIT` | `100` | Requests per window per API key or OAuth connection |
| `GRADLY_USER_RATE_LIMIT` | `150` | Requests per window per user, across all credentials |
| `GRADLY_RATE_LIMIT_WINDOW` | `5m` | Rolling window |
| `GRADLY_ACTIVITY_RETENTION` | `90d` | How long activity entries are kept (also capped at the newest 5,000 per user) |

**Rate-limit state** is kept in memory, which is exact for Gradly's single-process, single-SQLite-file deployment.
It restarts empty when the server restarts. Running several instances would need a shared store; Gradly's SQLite
storage doesn't support that layout anyway.

**Database.** The integration tables (`api_keys`, `o_auth_clients`, `o_auth_grants`, `o_auth_codes`, `o_auth_tokens`,
`integration_events`, `idempotency_records`) are created by the usual GORM auto-migration on start-up. Existing tables
and data are untouched. To roll the feature back, stop the server and drop those seven tables
(`models.IntegrationTables()` lists them in a safe order). Expired codes, tokens and idempotency records are removed
opportunistically when new ones are written; no background worker runs.

**Reverse proxies.** Pass the `Authorization` header through to `/mcp`, and don't buffer or cache `/mcp`, `/oauth/*`
or `/.well-known/*`.
