import {
  Button,
  PixelIcon,
  Select,
  StatusBar,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';
import { FileDetail } from './FileDetail';
import { DocumentEditor } from './DocumentEditor';
import { VersionComparison } from './VersionComparison';
import { EnvironmentEditor } from './ResourceEnvironment';
import { RequestReview } from '../reviews/RequestReview';
import { RunnerImport } from './RunnerImport';
import { useLibraryController } from './useLibraryController';

export function Library({ desktop, window }: AppWindowProps) {
  const {
    team,
    sourcesOpen,
    setSourcesOpen,
    importOpen,
    setImportOpen,
    sourcesId,
    sourcesToggle,
    importLauncher,
    sourcesPanel,
    closeSources,
    requestReview,
    setRequestReview,
    user,
    resources,
    drafts,
    rooms,
    resourceId,
    path,
    setPath,
    files,
    commit,
    publications,
    history,
    setHistory,
    environmentOpen,
    setEnvironmentOpen,
    version,
    setVersion,
    comparison,
    setComparison,
    editor,
    targetRoom,
    setTargetRoom,
    error,
    libraryError,
    filesError,
    loading,
    filesLoading,
    pending,
    invalidateEditorRequest,
    refresh,
    openLibraryActions,
    resource,
    reviewRoom,
    reference,
    selectResource,
    edit,
    copy,
    openDraft,
    openImport,
    toggleHistory,
    closeReview,
    closeEditor,
    selectFile,
    restore,
  } = useLibraryController({ desktop, window });
  return (
    <div className="camp-library">
      <div
        className="camp-library-body"
        onKeyDown={(event) => {
          if (event.key === 'Escape' && sourcesOpen) {
            event.preventDefault();
            event.stopPropagation();
            closeSources();
          }
        }}
      >
        {sourcesOpen && !importOpen && (
          <button
            className="camp-library-nav-backdrop"
            aria-label="Close Library sources"
            tabIndex={-1}
            onPointerDown={(event) => event.preventDefault()}
            onClick={closeSources}
          />
        )}
        <aside
          hidden={importOpen}
          className={`camp-resource-nav app-control-nav ${sourcesOpen ? 'is-open' : ''}`}
          aria-label="Library resources"
          id={sourcesId}
          ref={sourcesPanel}
          onBlur={(event) => {
            if (
              sourcesOpen &&
              event.relatedTarget instanceof Node &&
              !event.currentTarget.contains(event.relatedTarget)
            )
              setSourcesOpen(false);
          }}
        >
          <header className="camp-library-sources-heading">
            <h2>Sources</h2>
            <Button
              className="camp-library-sources-toggle"
              onClick={closeSources}
            >
              Done
            </Button>
          </header>
          <div className="camp-library-source-list">
            {[false, true].map((immutable) => (
              <section key={String(immutable)}>
                <h3>{immutable ? 'Originals' : 'Working files'}</h3>
                {resources
                  .filter((item) => item.immutable === immutable)
                  .map((item) => (
                    <button
                      key={item.id}
                      aria-current={resourceId === item.id ? 'true' : undefined}
                      className={resourceId === item.id ? 'selected' : ''}
                      onClick={() => void selectResource(item.id)}
                    >
                      <PixelIcon
                        name={immutable ? 'document' : 'folder'}
                        size={22}
                      />
                      <span>
                        {item.name}
                        <small>
                          {rooms.find((room) => room.resourceId === item.id)
                            ?.name ??
                            (immutable ? 'Read only' : 'Shared files')}
                        </small>
                      </span>
                    </button>
                  ))}
              </section>
            ))}
            {drafts.length > 0 && (
              <section>
                <h3>Your drafts</h3>
                {drafts.map((item) => (
                  <button key={item.key} onClick={() => void openDraft(item)}>
                    <PixelIcon name="notes" size={20} />
                    <span>
                      {item.path}
                      <small>Only you can see this draft</small>
                    </span>
                  </button>
                ))}
              </section>
            )}
          </div>
          <footer className="camp-library-sources-footer">
            <Button ref={importLauncher} onClick={() => void openImport()}>
              Import from Runner…
            </Button>
            {user.role === 'admin' && (
              <Button
                onClick={() =>
                  void team
                    .navigate({ app: 'settings', section: 'storage' })
                    .then((opened) => {
                      if (opened) setSourcesOpen(false);
                    })
                }
              >
                Storage Settings…
              </Button>
            )}
          </footer>
        </aside>
        <main className="camp-library-main">
          {!importOpen && (
            <>
              <div className="app-toolbar camp-library-toolbar">
                <Button
                  className="camp-library-sources-toggle"
                  ref={sourcesToggle}
                  aria-controls={sourcesId}
                  aria-expanded={sourcesOpen}
                  onClick={() => setSourcesOpen(!sourcesOpen)}
                >
                  Sources
                </Button>
                <PixelIcon name="folder" size={24} />
                <strong>{resource?.name ?? 'Library'}</strong>
                <div className="app-actions">
                  <Button
                    disabled={
                      importOpen ||
                      !resource ||
                      resource.immutable ||
                      pending ||
                      Boolean(version) ||
                      Boolean(requestReview)
                    }
                    onClick={() => void edit(true)}
                  >
                    New Document
                  </Button>
                  <Button
                    aria-pressed={history}
                    disabled={!resource || Boolean(requestReview) || importOpen}
                    onClick={() => void toggleHistory()}
                  >
                    History
                  </Button>
                  <Button
                    aria-label="More Library actions"
                    aria-haspopup="menu"
                    onClick={(event) => openLibraryActions(event.currentTarget)}
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowDown') return;
                      event.preventDefault();
                      openLibraryActions(event.currentTarget);
                    }}
                  >
                    More
                  </Button>
                </div>
              </div>
              {version && !history && !environmentOpen && (
                <div className="camp-version-banner" role="status">
                  <span>
                    <strong>
                      {version.commit === resource?.currentCommit
                        ? 'Saved version'
                        : 'Historical version'}{' '}
                      · read only
                    </strong>
                    {version.createdAt
                      ? ` · ${new Date(version.createdAt).toLocaleString()}`
                      : ''}
                  </span>
                  <Button
                    onClick={() => {
                      setVersion(null);
                      setPath('');
                    }}
                  >
                    Return to Latest
                  </Button>
                </div>
              )}
              {error && (
                <p className="app-error" role="alert">
                  {error}
                </p>
              )}
              {libraryError && (
                <p className="app-error" role="alert">
                  {libraryError}{' '}
                  <Button onClick={refresh}>Retry Library</Button>
                </p>
              )}
              {loading && !resources.length && (
                <div className="camp-empty" role="status">
                  Opening Library…
                </div>
              )}
            </>
          )}
          {importOpen ? (
            <RunnerImport
              onClose={(restoreFocus) => {
                setImportOpen(false);
                if (restoreFocus)
                  requestAnimationFrame(() => {
                    const target = importLauncher.current?.getClientRects()
                      .length
                      ? importLauncher.current
                      : sourcesToggle.current;
                    target?.focus({ preventScroll: true });
                  });
              }}
            />
          ) : requestReview ? (
            <div className="camp-library-review-request">
              <RequestReview
                key={`${requestReview.roomId}:${requestReview.resourceId}:${requestReview.path}`}
                {...requestReview}
                currentCommit={
                  resources.find((item) => item.id === requestReview.resourceId)
                    ?.currentCommit ?? requestReview.commit
                }
                onClose={() => void closeReview()}
                onCreated={(review) => {
                  void team
                    .navigate({ roomId: review.roomId, reviewId: review.id })
                    .then((opened) => {
                      if (opened) setRequestReview(null);
                    });
                }}
              />
            </div>
          ) : (
            resource &&
            (environmentOpen ? (
              <EnvironmentEditor
                key={resource.id}
                resource={resource}
                onChange={refresh}
                onClose={() => setEnvironmentOpen(false)}
              />
            ) : editor?.resourceId === resource.id ? (
              <DocumentEditor
                key={`${resource.id}:${editor.path}`}
                resource={resource}
                path={editor.path}
                content={editor.content}
                baseCommit={editor.baseCommit}
                onPublished={refresh}
                onClose={() => void closeEditor()}
              />
            ) : comparison ? (
              <VersionComparison
                key={comparison.id}
                resourceId={resource.id}
                publication={comparison}
                onClose={() => setComparison(null)}
              />
            ) : history ? (
              <section className="camp-library-history">
                <div className="camp-section-heading">
                  <h2>Shared version history</h2>
                  <Button onClick={() => setHistory(false)}>
                    Back to Files
                  </Button>
                </div>
                <p className="camp-library-history-description">
                  Saved changes to {resource.name}.
                </p>
                {filesError ? (
                  <p className="app-error" role="alert">
                    {filesError} <Button onClick={refresh}>Retry Files</Button>
                  </p>
                ) : !publications.length && !filesLoading ? (
                  <p>No published changes yet.</p>
                ) : null}
                {publications.map((item) => (
                  <article key={item.id}>
                    <div className="camp-library-version-heading">
                      <strong>
                        {item.kind === 'agent'
                          ? 'Agent result'
                          : item.kind === 'restore'
                            ? 'Restored version'
                            : item.path}
                      </strong>
                      {item.commit === resource.currentCommit && (
                        <span>Current version</span>
                      )}
                    </div>
                    <p>
                      {item.authorName || 'Agent'} ·{' '}
                      {new Date(item.createdAt).toLocaleString()} ·{' '}
                      {item.checksStatus === 'passed'
                        ? 'Checks passed'
                        : item.checksStatus === 'not_configured'
                          ? 'Checks not configured'
                          : 'Checks not run'}
                    </p>
                    <div className="app-actions">
                      <Button
                        onClick={() => {
                          setVersion({
                            commit: item.commit,
                            createdAt: item.createdAt,
                          });
                          setHistory(false);
                          setComparison(null);
                          setPath('');
                        }}
                      >
                        View Version
                      </Button>
                      {item.previousCommit && (
                        <Button onClick={() => setComparison(item)}>
                          Compare with Previous
                        </Button>
                      )}
                      {item.runId && (
                        <Button
                          onClick={() => {
                            const room = rooms.find(
                              (room) => room.resourceId === resource.id,
                            );
                            if (room)
                              void team.navigate({
                                roomId: room.id,
                                runId: item.runId,
                              });
                          }}
                        >
                          Open Conversation
                        </Button>
                      )}
                      {item.commit !== resource.currentCommit &&
                        !resource.immutable && (
                          <Button
                            disabled={pending}
                            onClick={() => void restore(item)}
                          >
                            Restore Version…
                          </Button>
                        )}
                    </div>
                  </article>
                ))}
              </section>
            ) : filesError ? (
              <div className="camp-empty" role="alert">
                <p>{filesError}</p>
                <Button onClick={refresh}>Retry Files</Button>
              </div>
            ) : filesLoading ||
              commit !== (version?.commit ?? resource.currentCommit) ? (
              <div className="camp-empty" role="status">
                Loading shared files…
              </div>
            ) : (
              <div className="camp-library-content">
                <nav className="camp-file-list" aria-label="Resource files">
                  <h2>
                    Files <span>{files.length}</span>
                  </h2>
                  {files.map((file) => (
                    <button
                      key={file}
                      className={file === path ? 'selected' : ''}
                      aria-current={file === path ? 'true' : undefined}
                      onClick={() => void selectFile(file)}
                    >
                      <PixelIcon name="document" size={18} />
                      <span>{file}</span>
                    </button>
                  ))}
                </nav>
                <div className="camp-file-selection">
                  {files.length > 0 && (
                    <div className="camp-file-picker">
                      <Select
                        label="File"
                        value={path}
                        onChange={(event) => {
                          invalidateEditorRequest();
                          setPath(event.target.value);
                        }}
                      >
                        {files.map((file) => (
                          <option key={file} value={file}>
                            {file}
                          </option>
                        ))}
                      </Select>
                    </div>
                  )}
                  {reference ? (
                    <FileDetail
                      key={`${resource.id}:${commit}:${path}`}
                      resource={resource}
                      reference={reference}
                      rooms={rooms}
                      targetRoom={targetRoom}
                      onTargetRoom={setTargetRoom}
                      historical={Boolean(version)}
                      pending={pending}
                      onEdit={() => void edit()}
                      onCopy={() => void copy()}
                      onReview={
                        !resource.immutable &&
                        (!version ||
                          version.commit === resource.currentCommit) &&
                        reviewRoom
                          ? () => {
                              invalidateEditorRequest();
                              setRequestReview({
                                roomId: reviewRoom.id,
                                resourceId: resource.id,
                                path,
                                commit,
                              });
                            }
                          : undefined
                      }
                    />
                  ) : (
                    <div className="camp-empty">
                      <PixelIcon name="folder" size={48} />
                      <h2>No files in {resource.name}</h2>
                      <p>
                        Create a document here, or upload source material in a
                        room and invite an agent to help.
                      </p>
                      <div className="app-actions">
                        {!resource.immutable && (
                          <Button
                            primary
                            disabled={pending}
                            onClick={() => void edit(true)}
                          >
                            Create Document
                          </Button>
                        )}
                        <Button
                          disabled={!targetRoom}
                          onClick={() =>
                            void team.navigate({ roomId: targetRoom })
                          }
                        >
                          Open a Room
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            ))
          )}
        </main>
      </div>
      {!importOpen && (
        <StatusBar>
          <span>Team Library</span>
          <span>
            {filesLoading
              ? 'Loading files…'
              : filesError
                ? 'Files unavailable'
                : `${files.length} ${files.length === 1 ? 'file' : 'files'}`}
            {resource?.immutable
              ? ' · Read only'
              : version
                ? version.commit === resource?.currentCommit
                  ? ' · Saved version'
                  : ' · Historical version'
                : ''}
          </span>
        </StatusBar>
      )}
    </div>
  );
}
