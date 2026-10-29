// Pending request bodies belong to this user's browser tab. Persist them before
// sending so an uncertain response can be retried after navigation or reload.
export function receiptKey(userId: string, operation: string, id: string) {
  return `camp:receipt:${userId}:${operation}:${id}`;
}

export function readSessionReceipt<T>(
  key: string,
  validate: (value: unknown) => value is T,
): T | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    const record = JSON.parse(raw) as { version?: unknown; value?: unknown };
    return record?.version === 1 && validate(record.value)
      ? record.value
      : null;
  } catch {
    return null;
  }
}

export function writeSessionReceipt<T>(key: string, value: T) {
  try {
    sessionStorage.setItem(key, JSON.stringify({ version: 1, value }));
  } catch {
    // Do not send a new request without retaining its recovery identity.
    throw new Error(
      'Camp could not retain this request in browser storage. Free space or enable tab storage, then retry.',
    );
  }
}

export function clearSessionReceipt(key: string) {
  try {
    sessionStorage.removeItem(key);
  } catch {
    // A retained confirmed receipt can only replay its original request.
  }
}
