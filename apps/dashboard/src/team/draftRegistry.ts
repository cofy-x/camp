const writers = new Map<string, () => Promise<boolean>>();

export function registerDraftWriter(
  key: string,
  flush: () => Promise<boolean>,
) {
  writers.set(key, flush);
  return () => {
    if (writers.get(key) === flush) writers.delete(key);
  };
}

export async function flushPrivateDrafts() {
  const results = await Promise.all(
    [...writers.values()].map((flush) => flush()),
  );
  return results.every(Boolean);
}
