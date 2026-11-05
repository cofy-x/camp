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
    viewVersion,
    compareVersion,
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
                desktop={desktop}
                showHeading={false}
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
              <VersionHistory
                key={resource.id}
                desktop={desktop}
                resource={resource}
                publications={publications}
                roomId={
                  rooms.find((room) => room.resourceId === resource.id)?.id
                }
                pending={pending}
                error={filesError}
                loading={filesLoading}
                onRetry={refresh}
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
                              team.cancelNavigation();
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
