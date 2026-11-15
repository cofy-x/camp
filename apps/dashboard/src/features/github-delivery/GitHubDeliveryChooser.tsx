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
  const edit = (change: Partial<GitHubDeliverySpec>) => {
    if (value) onChange({ ...value, ...change });
  };
  return (
    <div className="work-item-form">
      <Select
        label="Delivery"
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
        <option value="">Room files</option>
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
      {read.loading && !read.data && <p role="status">Loading repositories…</p>}
      {read.error && (
        <p className="app-error" role="alert">
          {read.error}
        </p>
      )}
      {!value && read.data && items.length === 0 && (
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
              <label>
                Release tag
                <input
                  className="p9-field"
                  required
                  value={value.tag ?? ''}
                  onChange={(event) => edit({ tag: event.target.value })}
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
          <p>Workflow changes are excluded.</p>
        </>
      )}
    </div>
  );
}
