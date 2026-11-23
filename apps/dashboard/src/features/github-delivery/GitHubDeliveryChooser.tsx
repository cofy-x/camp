import { Select, Checkbox, Button } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { useRead } from '../../shared/reads/useRead';
import type { GitHubDeliverySpec, GitHubRepository } from './api';

export function GitHubDeliveryChooser({
  value,
  onChange,
}: {
  value?: GitHubDeliverySpec;
  onChange: (value?: GitHubDeliverySpec) => void;
}) {
  const team = useTeam();
  const read = useRead<{ repositories: GitHubRepository[] }>(
    '/github-repositories',
  );
  const items = read.data?.repositories ?? [];
  const selected = items.find((item) => item.id === value?.connectionId);
  const needsConnection =
    !value && Boolean(read.data) && !items.some((item) => item.enabled);
  const edit = (change: Partial<GitHubDeliverySpec>) => {
    if (value) onChange({ ...value, ...change });
  };
  return (
    <div className="work-item-form">
      {!needsConnection && (
        <Select
          label="Repository"
          value={value?.connectionId ?? ''}
          onChange={(event) => {
            const item = items.find((item) => item.id === event.target.value);
            onChange(
              item
                ? {
                    connectionId: item.id,
                    connectionRevision: item.revision,
                    baseBranch: item.defaultBranch,
                    mode: 'pr',
                  }
                : undefined,
            );
          }}
        >
          <option value="" disabled>
            Choose a repository
          </option>
          {items
            .filter((item) => item.enabled || item.id === value?.connectionId)
            .map((item) => (
              <option key={item.id} value={item.id}>
                {item.repository}
                {item.private ? ' · Private' : ' · Public'}
                {item.enabled ? '' : ' · Disabled'}
              </option>
            ))}
          {value && !selected && (
            <option value={value.connectionId}>
              Saved repository unavailable
            </option>
          )}
        </Select>
      )}
      {read.loading && !read.data && <p role="status">Loading repositories…</p>}
      {read.error && (
        <p className="app-error" role="alert">
          {read.error}
        </p>
      )}
      {needsConnection && (
        <Button
          onClick={() =>
            void team.navigate({ app: 'settings', section: 'github' })
          }
        >
          Connect repository…
        </Button>
      )}
      {value && (
        <>
          {(!selected ||
            !selected.enabled ||
            selected.revision !== value.connectionRevision) && (
            <p className="app-error" role="alert">
              The repository connection changed. Select its current settings
              before starting.
              {selected?.enabled && (
                <Button
                  onClick={() =>
                    edit({ connectionRevision: selected.revision })
                  }
                >
                  Use current connection
                </Button>
              )}
            </p>
          )}
          <label>
            Target branch
            <input
              className="p9-field"
              required
              value={value.baseBranch}
              onChange={(event) => edit({ baseBranch: event.target.value })}
            />
          </label>
          <Select
            label="After the goal is met"
            value={value.mode}
            onChange={(event) =>
              edit({
                mode: event.target.value as GitHubDeliverySpec['mode'],
                tag: undefined,
                versionLine: undefined,
                maxRepairs: event.target.value === 'pr' ? 0 : 1,
                waitForDeployment: false,
              })
            }
          >
            <option value="pr">Open a pull request</option>
            <option value="merge" disabled={!selected?.ciWorkflowId}>
              Merge after CI
            </option>
            <option value="release" disabled={!selected?.ciWorkflowId}>
              Merge and publish a release
            </option>
          </Select>
          {value.mode === 'release' && (
            <>
              <Select
                label="Release version"
                value={value.versionLine !== undefined ? 'source' : 'exact'}
                onChange={(e) =>
                  edit(
                    e.target.value === 'source'
                      ? { versionLine: '', tag: undefined }
                      : { versionLine: undefined, tag: '' },
                  )
                }
              >
                <option value="exact">Exact tag</option>
                <option value="source">From package.json</option>
              </Select>
              <label>
                {value.versionLine !== undefined
                  ? 'Allowed release line (major.minor)'
                  : 'Release tag'}
                <input
                  className="p9-field"
                  required
                  value={value.versionLine ?? value.tag ?? ''}
                  onChange={(e) =>
                    edit(
                      value.versionLine !== undefined
                        ? { versionLine: e.target.value }
                        : { tag: e.target.value },
                    )
                  }
                />
              </label>
              <Checkbox
                label="Prerelease"
                checked={value.prerelease ?? false}
                onChange={(event) => edit({ prerelease: event.target.checked })}
              />
            </>
          )}
          {value.mode !== 'pr' && selected?.deploymentWorkflowId && (
            <Checkbox
              label="Wait for deployment"
              checked={value.waitForDeployment ?? false}
              onChange={(event) =>
                edit({ waitForDeployment: event.target.checked })
              }
            />
          )}
          {value.mode !== 'pr' && (
            <Checkbox
              label="Repair CI and approved review feedback automatically"
              checked={Boolean(value.maxRepairs)}
              onChange={(e) => edit({ maxRepairs: e.target.checked ? 1 : 0 })}
            />
          )}
          {Boolean(value.maxRepairs) && (
            <p>
              Up to {value.maxRepairs} repair{value.maxRepairs === 1 ? '' : 's'}{' '}
              within this Work's total allowance.
            </p>
          )}
          <p>Project ignore rules apply. Workflow changes are excluded.</p>
        </>
      )}
    </div>
  );
}
