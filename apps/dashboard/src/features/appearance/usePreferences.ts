import { useCallback, useMemo, useState } from 'react';
import { request } from '../../shared/api/http';
import type { Preferences } from './preferences';
import { createPreferenceQueue, defaultPreferences } from './preferences';

export function usePreferences() {
  const [preferences, setPreferences] = useState(defaultPreferences);
  const [queue] = useState(() =>
    createPreferenceQueue(defaultPreferences, (value) =>
      request<Preferences>('/preferences', {
        method: 'PUT',
        body: JSON.stringify(value),
      }),
    ),
  );
  const resetPreferences = useCallback(
    (value: Preferences) => {
      queue.reset(value);
      setPreferences(value);
    },
    [queue],
  );
  const savePreferences = useCallback(
    async (patch: Partial<Preferences>) => {
      const saved = await queue.save(patch);
      setPreferences(saved);
    },
    [queue],
  );
  const data = useMemo(
    () => ({ preferences, savePreferences }),
    [preferences, savePreferences],
  );
  return { data, resetPreferences };
}
