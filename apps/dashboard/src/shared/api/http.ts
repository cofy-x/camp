export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
  ) {
    super(message);
  }
}

export async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api${path}`, {
      ...init,
      headers: {
        ...(init.body && !(init.body instanceof FormData)
          ? { 'Content-Type': 'application/json' }
          : {}),
        ...init.headers,
      },
    });
  } catch (error) {
    if (
      init.signal?.aborted ||
      (error instanceof Error && error.name === 'AbortError')
    )
      throw error;
    throw new APIError(
      'Cannot reach the desktop server. Check your connection and try again.',
      0,
      'network_error',
    );
  }
  if (!response.ok) {
    if (response.status === 401 && !path.startsWith('/auth/'))
      window.dispatchEvent(new Event('camp:session-expired'));
    const body = await response
      .json()
      .catch(() => ({ error: response.statusText, code: 'http_error' }));
    throw new APIError(
      body.error || 'The server could not complete this operation.',
      response.status,
      body.code,
    );
  }
  return response.json() as Promise<T>;
}
