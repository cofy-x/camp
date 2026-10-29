import { useEffect, useId, useState } from 'react';
import { Button, Radio } from '@cofy-x/camp-platinum';
import { teamApi } from '../../team/api';
import { IdentityAvatar } from './IdentityAvatar';
import './identity.css';

export function AvatarPicker({
  kind,
  value,
  onChange,
  disabled,
  automatic = false,
}: {
  kind: 'member' | 'agent';
  value: string;
  onChange: (avatar: string) => void;
  disabled?: boolean;
  automatic?: boolean;
}) {
  const name = useId();
  const [catalog, setCatalog] = useState<Awaited<
    ReturnType<typeof teamApi.avatars>
  > | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setError('');
    void teamApi
      .avatars(controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setCatalog(result);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setError(
            error instanceof Error ? error.message : 'Could not load avatars.',
          );
      });
    return () => controller.abort();
  }, [attempt]);
  return (
    <fieldset className="camp-avatar-picker" disabled={disabled}>
      <legend>Avatar</legend>
      {error ? (
        <p className="app-error" role="alert">
          {error}{' '}
          <Button
            type="button"
            onClick={() => setAttempt((value) => value + 1)}
          >
            Retry Avatars
          </Button>
        </p>
      ) : !catalog ? (
        <p role="status">Loading avatars…</p>
      ) : (
        <div className="camp-avatar-options">
          {catalog.avatars
            .filter((entry) => entry.kind === kind)
            .map((entry) => (
              <Radio
                key={entry.id}
                name={name}
                value={entry.id}
                aria-label={entry.name}
                title={entry.name}
                checked={value === entry.id}
                onChange={() => onChange(entry.id)}
              >
                <IdentityAvatar avatar={entry.id} kind={kind} size={40} />
              </Radio>
            ))}
        </div>
      )}
      {automatic && (
        <Radio
          name={name}
          value=""
          label="Assign automatically"
          checked={!value}
          onChange={() => onChange('')}
        />
      )}
    </fieldset>
  );
}
