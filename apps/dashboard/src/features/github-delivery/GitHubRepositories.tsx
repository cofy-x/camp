import { useEffect, useId, useState } from 'react';
import { Button, Checkbox } from '@cofy-x/camp-platinum';
import { usePrivateDraft } from '../../team/usePrivateDraft';
import { registerDraftWriter } from '../../team/draftRegistry';
import { DraftRecovery } from '../../shared/drafts/DraftRecovery';
import { useRead } from '../../shared/reads/useRead';
import { APIError } from '../../shared/api/http';
import {
  githubDeliveryApi,
  type RepositoryInput,
  type GitHubRepository,
} from './api';

type Draft = { input: RepositoryInput; pending?: string } | null;

export function GitHubRepositories({
  onLockChange,
}: {
  onLockChange: (locked: boolean) => void;
}) {
  const id = useId();
  const read = useRead<{ repositories: GitHubRepository[] }>(
    '/github-repositories',
  );
  const draft = usePrivateDraft<Draft>(
    'conversation',
    'github-repository-settings',
    null,
  );
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const value = draft.value;
  const locked = Boolean(token) && !value?.pending;
  useEffect(() => {
    onLockChange(locked);
    return () => onLockChange(false);
  }, [locked, onLockChange]);
  useEffect(() => {
    if (!locked) return;
    const unregister = registerDraftWriter(
      `github-credential:${id}`,
      async () => {
        setError('Save or clear the credential edits before leaving.');
        return false;
      },
    );
    const protect = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', protect);
    return () => {
      unregister();
      window.removeEventListener('beforeunload', protect);
    };
  }, [id, locked]);
  const disabled =
    busy || !draft.loaded || Boolean(draft.error) || Boolean(value?.pending);

  function edit(change: Partial<RepositoryInput>) {
    if (value)
      draft.setValue({ ...value, input: { ...value.input, ...change } });
  }

  function choose(input: RepositoryInput) {
    if (token || value?.pending) {
      setError('Save or clear the credential edits first.');
      return;
    }
    setError('');
    draft.setValue({ input });
  }

  async function finish() {
    setToken('');
    draft.setValue(null);
    await draft.flush();
    await read.refresh();
  }

  async function save() {
    if (!value || disabled) return;
    setError('');
    setBusy(true);
    const requestId = crypto.randomUUID();
    draft.setValue({ ...value, pending: requestId });
    if (!(await draft.flush())) {
      setBusy(false);
      return;
    }
    try {
      await githubDeliveryApi.save({
        ...value.input,
        requestId,
        token: token || undefined,
      });
      await finish();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'The repository connection was not confirmed.',
      );
      if (
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500
      ) {
        draft.setValue({ ...value, pending: undefined });
        await draft.flush();
      }
    } finally {
      setBusy(false);
    }
  }

  async function recover(cancel = false) {
    const pending = value?.pending;
    if (!pending || busy) return;
    setBusy(true);
    setError('');
    try {
      if (cancel) await githubDeliveryApi.cancel(pending);
      else await githubDeliveryApi.receipt(pending);
      await finish();
    } catch (error) {
      if (error instanceof APIError && error.code === 'request_cancelled') {
        await finish();
      } else
        setError(
          error instanceof Error
            ? error.message
            : 'The saved operation could not be checked.',
        );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section
      className="camp-github-repositories"
      aria-label="GitHub repositories"
    >
      <DraftRecovery
        draft={draft}
        preview={(value) => value?.input.repository ?? ''}
      />
      {read.error && (
        <p role="alert" className="app-error">
          {read.error}
        </p>
      )}
      {!value && (
        <>
          <div className="app-actions">
            <Button
              disabled={disabled}
              onClick={() =>
                choose({
                  repository: '',
                  ciWorkflow: '',
                  deploymentWorkflow: '',
                  enabled: true,
                })
              }
            >
              Connect repository…
            </Button>
          </div>
          {read.loading && !read.data ? (
            <p role="status">Loading repositories…</p>
          ) : read.data?.repositories.length === 0 ? (
            <p>No repositories connected.</p>
          ) : (
            <ul className="camp-repository-list">
              {read.data?.repositories.map((item) => (
                <li key={item.id}>
                  <span>
                    {item.repository} · {item.private ? 'Private' : 'Public'}
                    {item.enabled ? '' : ' · Disabled'}
                  </span>
                  <Button
                    onClick={() =>
                      choose({
                        id: item.id,
                        revision: item.revision,
                        repository: item.repository,
                        ciWorkflow: item.ciWorkflow ?? '',
                        deploymentWorkflow: item.deploymentWorkflow ?? '',
                        enabled: item.enabled,
                      })
                    }
                  >
                    Edit…
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {value && (
        <form
          className="camp-repository-form"
          aria-label="Repository connection"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <fieldset disabled={disabled}>
            <label htmlFor={id}>Repository</label>
            <input
              id={id}
              className="p9-field"
              required
              placeholder="owner/repository"
              value={value.input.repository}
              onChange={(event) => edit({ repository: event.target.value })}
            />
            <label htmlFor={id + '-token'}>
              GitHub token{value.input.id ? ' (leave blank to keep saved)' : ''}
            </label>
            <input
              id={id + '-token'}
              className="p9-field"
              type="password"
              autoComplete="off"
              required={!value.input.id}
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
            <label htmlFor={id + '-ci'}>Required CI workflow</label>
            <input
              id={id + '-ci'}
              className="p9-field"
              placeholder="ci.yml"
              value={value.input.ciWorkflow ?? ''}
              onChange={(event) => edit({ ciWorkflow: event.target.value })}
            />
            <label htmlFor={id + '-deployment'}>
              Deployment workflow (optional)
            </label>
            <input
              id={id + '-deployment'}
              className="p9-field"
              placeholder="deploy.yml"
              value={value.input.deploymentWorkflow ?? ''}
              onChange={(event) =>
                edit({ deploymentWorkflow: event.target.value })
              }
            />
            <Checkbox
              label="Enabled"
              checked={value.input.enabled}
              onChange={(event) => edit({ enabled: event.target.checked })}
            />
          </fieldset>
          <p>
            Repository-restricted token: Contents and Pull requests (write),
            Actions (read). Stored privately on the Server.
          </p>
          {value.pending ? (
            <div role="status">
              <p>Connection change not confirmed.</p>
              <div className="app-actions">
                <Button disabled={busy} onClick={() => void recover()}>
                  Check status
                </Button>
                <Button disabled={busy} onClick={() => void recover(true)}>
                  Cancel pending change
                </Button>
              </div>
            </div>
          ) : (
            <div className="app-actions">
              <Button type="submit" primary disabled={disabled}>
                {busy ? 'Saving…' : 'Save connection'}
              </Button>
              <Button
                disabled={busy}
                onClick={() => {
                  setToken('');
                  draft.setValue(null);
                }}
              >
                Cancel
              </Button>
            </div>
          )}
          {token && (
            <Button onClick={() => setToken('')}>Clear credential edits</Button>
          )}
        </form>
      )}
      {error && (
        <p role="alert" className="app-error">
          {error}
        </p>
      )}
    </section>
  );
}
