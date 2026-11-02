import { useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { githubApi, type GitHubSource } from './api';
import { useGitHubOperation } from './useGitHubOperation';

function Source({ source }: { source: GitHubSource }) {
  const { user } = useAccount();
  const operation = useGitHubOperation(source.id);
  const [secret, setSecret] = useState('');
  return (
    <li>
      <div className="github-source-heading">
        <strong>{source.repository}</strong>
        <span>{source.enabled ? 'Enabled' : 'Disabled'}</span>
      </div>
      {user.role === 'admin' && (
        <>
          <div className="app-actions">
            <Button
              disabled={operation.blocked}
              onClick={() =>
                void operation.run((requestId) =>
                  githubApi.updateSource(source.id, {
                    requestId,
                    revision: source.revision,
                    enabled: !source.enabled,
                  }),
                )
              }
            >
              {source.enabled ? 'Disable Source' : 'Enable Source'}
            </Button>
          </div>
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
            <p>
              In this repository’s GitHub Webhooks settings, choose
              application/json and subscribe to Issues, Pull requests and Issue
              comments. Use the same secret and keep SSL verification enabled.
            </p>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void operation
                  .run((requestId) =>
                    githubApi.updateSource(source.id, {
                      requestId,
                      revision: source.revision,
                      secret,
                    }),
                  )
                  .then((saved) => {
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
            <p>
              Disabling stops new deliveries and confirmations. Already
              authorized collaborations keep their own controls. The repository
              name is fixed; its numeric identity is pinned on the first valid
              delivery.
            </p>
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
  error,
}: {
  roomId: string;
  sources: GitHubSource[];
  error: string;
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
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      <ul>
        {sources.map((source) => (
          <Source key={source.id} source={source} />
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
          <p>
            Use a random secret of 32–256 bytes. It is stored privately on
            Server and is never returned.
          </p>
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
