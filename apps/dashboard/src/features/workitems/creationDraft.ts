function canonical(value: unknown) {
  return JSON.stringify(value, (_key, entry: unknown) =>
    entry && typeof entry === 'object' && !Array.isArray(entry)
      ? Object.fromEntries(
          Object.entries(entry).sort(([left], [right]) =>
            left < right ? -1 : left > right ? 1 : 0,
          ),
        )
      : entry,
  );
}

// Private JSON may return with reordered keys after storage. Cleanup must
// recognize the submitted draft without treating another draft as expendable.
export function matchesCreationDraft(current: unknown, snapshot?: string) {
  if (snapshot === undefined) return false;
  try {
    return canonical(current) === canonical(JSON.parse(snapshot));
  } catch {
    return false;
  }
}
