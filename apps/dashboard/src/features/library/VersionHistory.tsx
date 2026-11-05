import { Button, type DesktopAPI, type MenuItem } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import type { LibraryResource, Publication } from './api';
import {
  versionChecksLabel,
  visibleVersionChecks,
} from './versionPresentation';

export function VersionHistory({
  desktop,
  resource,
  publications,
  roomId,
  pending,
  error,
  loading,
  onRetry,
  onView,
  onCompare,
  onRestore,
}: {
  desktop: DesktopAPI;
  resource: LibraryResource;
  publications: Publication[];
  roomId?: string;
  pending: boolean;
  error: string;
  loading: boolean;
  onRetry: () => void;
  onView: (publication: Publication, resourceId: string) => void;
  onCompare: (publication: Publication, resourceId: string) => void;
  onRestore: (
    publication: Publication,
    resource: LibraryResource,
  ) => Promise<void>;
}) {
  const team = useTeam();

  function more(button: HTMLButtonElement, publication: Publication) {
    // Menu commands retain the exact source and version that opened them.
    const selected = { ...publication };
    const source = { ...resource };
    const conversationRoomId = roomId;
    const items: MenuItem[] = [];
    if (selected.previousCommit)
      items.push({
        label: 'Compare with Previous',
        action: () => onCompare(selected, source.id),
      });
    if (selected.runId && conversationRoomId)
      items.push({
        label: 'Open Work Details',
        action: () =>
          void team.navigate({
            roomId: conversationRoomId,
            runId: selected.runId,
          }),
      });
    items.push({
      label: 'Version Details…',
      action: () =>
        void desktop.dialog({
          title: 'Version Details',
          message: (
            <dl className="camp-version-details">
              <dt>Source</dt>
              <dd>{source.name}</dd>
              <dt>Author</dt>
              <dd>{selected.authorName || 'Agent'}</dd>
              <dt>Saved</dt>
              <dd>{new Date(selected.createdAt).toLocaleString()}</dd>
              <dt>Checks</dt>
              <dd>{versionChecksLabel(selected.checksStatus)}</dd>
              <dt>Version</dt>
              <dd>
                <code>{selected.commit}</code>
              </dd>
            </dl>
          ),
          buttons: [
            { label: 'Done', value: 'done', default: true, cancel: true },
          ],
        }),
    });
    if (selected.commit !== source.currentCommit && !source.immutable)
      items.push(
        { separator: true },
        {
          label: 'Restore Version…',
          disabled: pending,
          action: () => void onRestore(selected, source),
        },
      );
    button.focus({ preventScroll: true });
    const bounds = button.getBoundingClientRect();
    desktop.contextMenu(items, { x: bounds.left, y: bounds.bottom });
  }

  return (
    <section
      className="camp-library-history"
      aria-label="Shared version history"
    >
      <div className="camp-section-heading">
        <h2>Shared version history</h2>
      </div>
      {error ? (
        <p className="app-error" role="alert">
          {error} <Button onClick={onRetry}>Retry Files</Button>
        </p>
      ) : !publications.length ? (
        <p role={loading ? 'status' : undefined}>
          {loading ? 'Loading history…' : 'No published changes yet.'}
        </p>
      ) : null}
      {publications.map((publication) => {
        const checks = visibleVersionChecks(publication.checksStatus);
        return (
          <article key={publication.id}>
            <div className="camp-library-version-row">
              <div className="camp-library-version-info">
                <div className="camp-library-version-heading">
                  <strong>
                    {publication.kind === 'agent'
                      ? 'Agent result'
                      : publication.kind === 'restore'
                        ? 'Restored version'
                        : publication.path}
                  </strong>
                  {publication.commit === resource.currentCommit && (
                    <span>Current version</span>
                  )}
                </div>
                <p className="camp-library-version-meta">
                  {publication.authorName || 'Agent'} ·{' '}
                  <time dateTime={publication.createdAt}>
                    {new Date(publication.createdAt).toLocaleString()}
                  </time>
                  {checks && <> · {checks}</>}
                </p>
              </div>
              <div className="app-actions">
                <Button onClick={() => onView(publication, resource.id)}>
                  View Version
                </Button>
                <Button
                  aria-label="More version actions"
                  aria-haspopup="menu"
                  onClick={(event) => more(event.currentTarget, publication)}
                  onKeyDown={(event) => {
                    if (event.key !== 'ArrowDown') return;
                    event.preventDefault();
                    more(event.currentTarget, publication);
                  }}
                >
                  More
                </Button>
              </div>
            </div>
          </article>
        );
      })}
    </section>
  );
}
