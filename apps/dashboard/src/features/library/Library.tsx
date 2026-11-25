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
import { VersionHistory } from './VersionHistory';
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
    libraryActions,
    sourcesPanel,
    closeSources,
    requestReview,
    setRequestReview,
    user,
    resources,
    drafts,
    draftsError,
    retryDrafts,
    rooms,
    resourceId,
    path,
    files,
    commit,
    publications,
    history,
    environmentOpen,
    setEnvironmentOpen,
    version,
    returnToLatest,
    comparison,
    setComparison,
    editor,
    contextRoomId,
    reviewsOpen,
    closeFileReviews,
    error,
    libraryError,
    filesError,
    loading,
    filesLoading,
    historyLoading,
    historyError,
    retryHistory,
    pending,
    publicationOperation,
    refresh,
    openLibraryActions,
    resource,
    reference,
    selectResource,
    edit,
    openDraft,
    openImport,
    toggleHistory,
    closeReview,
    closeEditor,
    selectFile,
    viewVersion,
    compareVersion,
    restore,
  } = useLibraryController({ desktop, window });
  const configuring = environmentOpen && Boolean(resource);
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
        {sourcesOpen && !importOpen && !configuring && (
          <button
            className="camp-library-nav-backdrop"
            aria-label="Close Library sources"
            tabIndex={-1}
            onPointerDown={(event) => event.preventDefault()}
            onClick={closeSources}
          />
        )}
        <aside
          hidden={importOpen || configuring}
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
            {[false, true].map((immutable) => {
              const group = resources.filter(
                (item) => item.immutable === immutable,
              );
              if (group.length === 0) return null;
              return (
                <section key={String(immutable)}>
                  <h3>{immutable ? 'Originals' : 'Working files'}</h3>
                  {group.map((item) => {
                    const room = rooms.find(
                      (room) => room.resourceId === item.id,
                    );
                    return (
                      <button
                        key={item.id}
                        aria-current={
                          resourceId === item.id ? 'true' : undefined
                        }
                        className={resourceId === item.id ? 'selected' : ''}
                        onClick={() => void selectResource(item.id)}
                      >
                        <PixelIcon
                          name={immutable ? 'document' : 'folder'}
                          size={22}
                        />
                        <span>
                          {item.name}
                          {room &&
                            item.name.toLowerCase() !==
                              `${room.name} files`.toLowerCase() && (
                              <small>{room.name}</small>
                            )}
                        </span>
                      </button>
                    );
                  })}
                </section>
              );
            })}
            {drafts.length > 0 && (
              <section>
                <h3>Your drafts</h3>
                {drafts.map((item) => (
                  <button key={item.key} onClick={() => void openDraft(item)}>
                    <PixelIcon name="notes" size={20} />
                    <span>{item.path}</span>
                  </button>
                ))}
              </section>
            )}
            {draftsError && (
              <p className="app-error" role="alert">
                {draftsError}{' '}
                <Button onClick={() => void retryDrafts()}>Retry Drafts</Button>
              </p>
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
              <div
                className="app-toolbar camp-library-toolbar"
                hidden={configuring}
              >
                <Button
                  className="camp-library-sources-toggle"
                  ref={sourcesToggle}
                  aria-controls={sourcesId}
                  aria-expanded={sourcesOpen}
                  aria-pressed={sourcesOpen}
                  title={
                    sourcesOpen
                      ? 'Hide sources sidebar'
                      : 'Show sources sidebar'
                  }
                  onClick={() => setSourcesOpen(!sourcesOpen)}
                >
                  <svg
                    className="camp-library-sidebar-icon"
                    width="16"
                    height="12"
                    viewBox="0 0 16 12"
                    aria-hidden="true"
                    focusable="false"
                    shapeRendering="crispEdges"
                  >
                    <rect
                      x="0.5"
                      y="0.5"
                      width="15"
                      height="11"
                      fill="none"
                      stroke="currentColor"
                    />
                    <path d="M5.5 1v10" stroke="currentColor" />
                    <path
                      className="camp-library-sidebar-pane"
                      d="M2 2h2v8H2z"
                      fill="currentColor"
                    />
                  </svg>
                  Sources
                </Button>
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
                    disabled={!resource || Boolean(requestReview) || importOpen}
                    onClick={() => void toggleHistory()}
                  >
                    {history ? 'Back to Files' : 'History'}
                  </Button>
                  <Button
                    ref={libraryActions}
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
              {(publicationOperation.pending || publicationOperation.error) && (
                <div className="app-actions">
                  <span role={publicationOperation.busy ? 'status' : 'alert'}>
                    {publicationOperation.pending &&
                      (publicationOperation.busy
                        ? 'Confirming publication… '
                        : publicationOperation.pending.kind === 'copy'
                          ? 'Working copy save unconfirmed. '
                          : 'Restore unconfirmed. ')}
                    {publicationOperation.error}
                  </span>
                  {publicationOperation.pending && (
                    <Button
                      disabled={publicationOperation.busy}
                      onClick={() => void publicationOperation.check()}
                    >
                      Check Saved Publication
                    </Button>
                  )}
                  {publicationOperation.missing && (
                    <Button
                      disabled={publicationOperation.busy}
                      onClick={() => void publicationOperation.retry()}
                    >
                      Retry Original Request
                    </Button>
                  )}
                </div>
              )}
              {publicationOperation.confirmed && (
                <div className="app-actions">
                  <span role="status">
                    {publicationOperation.confirmed.kind === 'copy'
                      ? 'Working copy saved.'
                      : 'Shared files restored.'}
                  </span>
                  <Button
                    onClick={() => {
                      const saved = publicationOperation.confirmed?.publication;
                      if (!saved) return;
                      void team
                        .navigate({
                          app: 'library',
                          resourceId: saved.resourceId,
                          commit: saved.commit,
                          path: saved.path,
                        })
                        .then((opened) => {
                          if (opened) publicationOperation.dismiss();
                        });
                    }}
                  >
                    View Saved Version
                  </Button>
                </div>
              )}
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
                  <Button onClick={returnToLatest}>Return to Latest</Button>
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
                key={`${requestReview.roomId}:${requestReview.resourceId}:${requestReview.path}:${requestReview.commit}`}
                {...requestReview}
                desktop={desktop}
                showHeading={false}
                currentCommit={
                  resources.find((item) => item.id === requestReview.resourceId)
                    ?.currentCommit
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
                onClose={() => {
                  setEnvironmentOpen(false);
                  requestAnimationFrame(() =>
                    libraryActions.current?.focus({ preventScroll: true }),
                  );
                }}
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
              <VersionHistory
                key={resource.id}
                desktop={desktop}
                resource={resource}
                publications={publications}
                roomId={
                  rooms.find((room) => room.resourceId === resource.id)?.id
                }
                pending={pending || Boolean(publicationOperation.pending)}
                error={historyError}
                loading={historyLoading}
                onRetry={retryHistory}
                onView={viewVersion}
                onCompare={compareVersion}
                onRestore={restore}
              />
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
                <nav
                  className="camp-file-list"
                  aria-label="Resource files"
                  hidden={!files.length}
                >
                  <h2>Files</h2>
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
                  {reference ? (
                    <FileDetail
                      resource={resource}
                      reference={reference}
                      reviewsOpen={reviewsOpen}
                      onCloseReviews={closeFileReviews}
                      contextRoomId={contextRoomId}
                      historical={Boolean(version)}
                      pending={pending}
                      filePicker={
                        <Select
                          aria-label="File"
                          value={path}
                          onChange={(event) =>
                            void selectFile(event.target.value)
                          }
                        >
                          {files.map((file) => (
                            <option key={file} value={file}>
                              {file}
                            </option>
                          ))}
                        </Select>
                      }
                      onEdit={() => void edit()}
                    />
                  ) : (
                    <div className="camp-library-empty">
                      <PixelIcon name="folder" />
                      <p>No files yet</p>
                    </div>
                  )}
                </div>
              </div>
            ))
          )}
        </main>
      </div>
      {!importOpen && !configuring && (
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
