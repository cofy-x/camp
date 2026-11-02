import { useState } from 'react';
import { Button, Checkbox } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { githubApi, type GitHubSource } from './api';
import { useGitHubOperation } from './useGitHubOperation';

function Source({
  source: observed,
  showIdentity,
}: {
  source: GitHubSource;
  showIdentity: boolean;
}) {
  const { user } = useAccount();
  const [confirmed, setConfirmed] = useState<GitHubSource | null>(null);
  const [proposedEnabled, setProposedEnabled] = useState<boolean | null>(null);
  const source =
    confirmed && confirmed.revision > observed.revision ? confirmed : observed;
  const operation = useGitHubOperation(source.id);
  const [secret, setSecret] = useState('');

  async function save(change: { enabled?: boolean; secret?: string }) {
    return operation.run(async (requestId) => {
      const result = await githubApi.updateSource(source.id, {
        requestId,
        revision: source.revision,
        ...change,
      });
      setConfirmed(result.source);
    });
  }

  return (
    <li aria-busy={operation.busy}>
      <div className="github-source-heading">
        <strong>{source.repository}</strong>
        {user.role === 'admin' ? (
          <Checkbox
            label="Enabled"
            checked={proposedEnabled ?? source.enabled}
            disabled={operation.blocked}
            onChange={(event) => {
              const enabled = event.target.checked;
              setProposedEnabled(enabled);
              void save({ enabled }).finally(() => setProposedEnabled(null));
            }}
          />
        ) : (
          <span>{source.enabled ? 'Enabled' : 'Disabled'}</span>
        )}
      </div>
      {showIdentity && source.repositoryId > 0 && (
        <small>Repository ID {source.repositoryId}</small>
      )}
      {user.role === 'admin' && (
        <>
          <details>
            <summary>Webhook setup</summary>
            <label>
              Payload URL
              <input
                className="p9-field"
                readOnly
                value={`${window.location.origin}/api/hooks/github/${source.id}`}
                onFocus={(event) => event.target.select()}
              />
            </label>
            <dl className="github-facts">
              <dt>Content type</dt>
              <dd>application/json</dd>
              <dt>Events</dt>
              <dd>Issues, Pull requests, Issue comments</dd>
              <dt>SSL verification</dt>
              <dd>Keep enabled on GitHub</dd>
              <dt>Repository ID</dt>
              <dd>{source.repositoryId || 'Not received yet'}</dd>
            </dl>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void save({ secret }).then((saved) => {
                  if (saved) setSecret('');
                });
              }}
            >
              <label>
                Replacement webhook secret
                <input
                  className="p9-field"
                  type="password"
                  autoComplete="new-password"
                  minLength={32}
                  maxLength={256}
                  required
                  value={secret}
                  disabled={operation.blocked}
                  onChange={(event) => setSecret(event.target.value)}
                />
              </label>
              <Button
                type="submit"
                disabled={operation.blocked || secret.length < 32}
              >
                Rotate Secret
              </Button>
            </form>
          </details>
          {operation.feedback}
        </>
      )}
    </li>
  );
}

export function GitHubSources({
  roomId,
  sources,
}: {
  roomId: string;
  sources: GitHubSource[];
}) {
  const { user } = useAccount();
  const operation = useGitHubOperation(`create:${roomId}`);
  const [expanded, setExpanded] = useState(!sources.length);
  const [repository, setRepository] = useState('');
  const [secret, setSecret] = useState('');
  return (
    <details
      className="github-sources"
      open={expanded}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary>Sources ({sources.length})</summary>
      <ul>
        {sources.map((source) => (
          <Source
            key={source.id}
            source={source}
            showIdentity={
              sources.filter((item) => item.repository === source.repository)
                .length > 1
            }
          />
        ))}
      </ul>
      {user.role === 'admin' ? (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void operation
              .run((requestId) =>
                githubApi.createSource(roomId, {
                  requestId,
                  repository,
                  secret,
                }),
              )
              .then((saved) => {
                if (saved) {
                  setRepository('');
                  setSecret('');
                }
              });
          }}
        >
          <h3>Add GitHub Source</h3>
          <label>
            Repository
            <input
              className="p9-field"
              placeholder="owner/repository"
              required
              maxLength={140}
              value={repository}
              disabled={operation.blocked}
              onChange={(event) => setRepository(event.target.value)}
            />
          </label>
          <label>
            Webhook secret
            <input
              className="p9-field"
              type="password"
              autoComplete="new-password"
              required
              minLength={32}
              maxLength={256}
              value={secret}
              disabled={operation.blocked}
              onChange={(event) => setSecret(event.target.value)}
            />
          </label>
          <p>
            Events and original payloads are visible to everyone in this team.
          </p>
          <p>Use a random secret of 32–256 bytes.</p>
          <Button type="submit" disabled={operation.blocked}>
            {operation.busy ? 'Saving…' : 'Add Source'}
          </Button>
          {operation.feedback}
        </form>
      ) : (
        !sources.length && (
          <p>An administrator can connect a GitHub repository to this Room.</p>
        )
      )}
    </details>
  );
}
