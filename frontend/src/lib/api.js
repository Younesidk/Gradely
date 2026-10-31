// Thin fetch wrapper for the JSON API. Every request carries a custom header, which is what makes the API
// immune to cross-site form posts (browsers never attach it to a cross-origin request without a preflight).

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

let unauthorizedHandler = () => {};

/** Called when a signed-in session turns out to be gone (expired, disabled, signed out elsewhere). */
export function onUnauthorized(fn) {
  unauthorizedHandler = fn;
}

async function request(method, path, body) {
  let res;
  try {
    res = await fetch('/api' + path, {
      method,
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-Gradely': '1' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("Couldn't reach the server. Check your connection and try again.", 0);
  }
  let data = null;
  try {
    data = await res.json();
  } catch {
    /* empty or non-JSON body */
  }
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) unauthorizedHandler();
    throw new ApiError(data?.error ?? 'Something went wrong. Please try again.', res.status);
  }
  return data;
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body = {}) => request('POST', path, body),
  put: (path, body = {}) => request('PUT', path, body),
  del: (path) => request('DELETE', path),
};
