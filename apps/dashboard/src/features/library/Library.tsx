import { useCallback, useEffect, useId, useRef, useState } from 'react';
import {
  Button,
  PixelIcon,
  Select,
  StatusBar,
  type AppWindowProps,
  type MenuItem,
} from '@cofy-x/camp-platinum';
import { libraryApi, type LibraryResource, type Publication } from './api';
import { workspaceApi, type Reference, type Room } from '../workspace/api';
import { FileDetail } from './FileDetail';
import { DocumentEditor } from './DocumentEditor';
import { useTeam } from '../../team/TeamContext';
import { useAccount } from '../../team/AuthContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { teamApi } from '../../team/api';
import { readTextFile } from './textFile';
import { VersionComparison } from './VersionComparison';
import { EnvironmentEditor } from './ResourceEnvironment';
import { RequestReview } from '../reviews/RequestReview';
import { RunnerImport } from './RunnerImport';

export function Library({ desktop, window }: AppWindowProps) {
  const team = useTeam();
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const sourcesId = useId();
  const sourcesToggle = useRef<HTMLButtonElement>(null);
  const importLauncher = useRef<HTMLButtonElement>(null);
  const sourcesPanel = useRef<HTMLElement>(null);
  const closeSources = () => {
    setSourcesOpen(false);
    if (
      sourcesPanel.current?.contains(document.activeElement) &&
      sourcesToggle.current?.getClientRects().length
    )
      sourcesToggle.current.focus({ preventScroll: true });
  };
  useEffect(() => {
    if (!sourcesOpen) return;
    const selected = sourcesPanel.current?.querySelector<HTMLButtonElement>(
      '[aria-current="true"]',
    );
    const target = selected ?? sourcesPanel.current?.querySelector('button');
    target?.focus({ preventScroll: true });
    selected?.scrollIntoView({ block: 'nearest' });
  }, [sourcesOpen]);
  const [requestReview, setRequestReview] = useState<{
    roomId: string;
    resourceId: string;
    path: string;
    commit: string;
  } | null>(null);
  const appliedNavigation = useRef(0);
  const selectedResource = useRef(window.params.resourceId ?? '');
  const { user } = useAccount();
  const [resources, setResources] = useState<LibraryResource[]>([]);
  const [drafts, setDrafts] = useState<
    Awaited<ReturnType<typeof teamApi.drafts>>['drafts']
  >([]);
  const [rooms, setRooms] = useState<Room[]>([]);
  const [resourceId, setResourceId] = useState(window.params.resourceId ?? '');
  const [path, setPath] = useState('');
  const [files, setFiles] = useState<string[]>([]);
  const [commit, setCommit] = useState('');
  const [publications, setPublications] = useState<Publication[]>([]);
  const [history, setHistory] = useState(false);
  const [environmentOpen, setEnvironmentOpen] = useState(false);
  const [version, setVersion] = useState<{
    commit: string;
    createdAt?: string;
  } | null>(null);
  const [comparison, setComparison] = useState<Publication | null>(null);
  const [editor, setEditor] = useState<{
    resourceId: string;
    path: string;
    content: string;
    baseCommit: string;
  } | null>(null);
  const [targetRoom, setTargetRoom] = useState('');
  const [error, setError] = useState('');
  const [libraryError, setLibraryError] = useState('');
  const [filesError, setFilesError] = useState('');
  const [loading, setLoading] = useState(true);
  const [filesLoading, setFilesLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [revision, setRevision] = useState(0);
  const editorRequest = useRef(0);
  const editorController = useRef<AbortController | null>(null);
  const filesErrorScope = useRef('');
  const invalidateEditorRequest = useCallback(() => {
    editorRequest.current += 1;
    if (editorController.current) {
      editorController.current.abort();
      editorController.current = null;
      setPending(false);
    }
  }, []);
  useEffect(
    () => () => {
      editorRequest.current += 1;
      editorController.current?.abort();
    },
    [],
  );
  const refresh = () => {
    setRevision((value) => value + 1);
    team.refresh();
  };
  const openLibraryActions = (button: HTMLButtonElement) => {
    const items: MenuItem[] = [];
    if (user.role === 'admin' && resource && !resource.immutable) {
      items.push(
        {
          label: 'Environment…',
          disabled: Boolean(requestReview),
          action: () =>
            void flushPrivateDrafts().then((saved) => {
              if (!saved) return;
              invalidateEditorRequest();
              setEditor(null);
              setHistory(false);
              setComparison(null);
              setEnvironmentOpen(true);
            }),
        },
        { separator: true },
      );
    }
    items.push({ label: 'Refresh', action: refresh });
    const rect = button.getBoundingClientRect();
    desktop.contextMenu(items, { x: rect.left, y: rect.bottom });
  };
  useEffect(
    () => desktop.beforeClose(window.id, flushPrivateDrafts),
    [desktop, window.id],
  );
  useEffect(() => {
    void teamApi
      .drafts('document')
      .then((result) => setDrafts(result.drafts))
      .catch(() => {});
  }, [revision]);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    void libraryApi
      .list(controller.signal)
      .then((result) => {
        if (controller.signal.aborted) return;
        setLoading(false);
        setLibraryError('');
        setResources(result.resources);
        setRooms(result.rooms);
        setResourceId((current) =>
          result.resources.some((item) => item.id === current)
            ? current
            : (result.resources.find((item) => !item.immutable)?.id ??
              result.resources[0]?.id ??
              ''),
        );
        setTargetRoom((current) =>
          result.rooms.some((item) => item.id === current)
            ? current
            : (result.rooms.find(
                (room) =>
                  room.resourceId ===
                  (selectedResource.current ||
                    result.resources.find((item) => !item.immutable)?.id),
              )?.id ??
              result.rooms[0]?.id ??
              ''),
        );
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setLoading(false);
          setLibraryError(
            error instanceof Error
              ? error.message
              : 'Could not load the library.',
          );
        }
      });
    return () => controller.abort();
  }, [team.revision, revision]);
  useEffect(() => {
    const intent = team.navigation;
    if (
      intent?.app !== 'library' ||
      !rooms.length ||
      appliedNavigation.current === intent.revision
    )
      return;
    appliedNavigation.current = intent.revision;
    invalidateEditorRequest();
    const context =
      rooms.find((room) => room.id === intent.contextRoomId) ??
      rooms.find((room) => room.resourceId === intent.resourceId);
    if (context) setTargetRoom(context.id);
    setSourcesOpen(false);
    setRequestReview(null);
    setImportOpen(false);
    setEnvironmentOpen(false);
    setRevision((value) => value + 1);
    setVersion(intent.commit ? { commit: intent.commit } : null);
    setComparison(null);
    if (intent.resourceId) {
      setResourceId(intent.resourceId);
      setEditor(null);
      setHistory(false);
      setFiles([]);
      setCommit('');
    }
    if (intent.path) setPath(intent.path);
  }, [team.navigation, rooms, invalidateEditorRequest]);
  const resource = resources.find((item) => item.id === resourceId);
  useEffect(() => {
    selectedResource.current = resourceId;
  }, [resourceId]);
  const reviewRoom =
    rooms.find(
      (item) => item.resourceId === resourceId && item.id === targetRoom,
    ) ?? rooms.find((item) => item.resourceId === resourceId);
  useEffect(() => {
    if (!resourceId) return;
    const controller = new AbortController();
    setFilesLoading(true);
    const scope = JSON.stringify([resourceId, version?.commit]);
    if (filesErrorScope.current !== scope) {
      filesErrorScope.current = scope;
      setFilesError('');
    }
    void Promise.all([
      workspaceApi.files(resourceId, controller.signal, version?.commit),
      libraryApi.history(resourceId, controller.signal),
    ])
      .then(([listing, changes]) => {
        if (controller.signal.aborted) return;
        setFilesLoading(false);
        setFilesError('');
        setFiles(listing.files);
        setCommit(listing.commit);
        setPublications(changes.publications);
        setPath((current) =>
          listing.files.includes(current) ? current : (listing.files[0] ?? ''),
        );
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setFilesLoading(false);
          setFilesError(
            error instanceof Error
              ? error.message
              : 'Could not read these files.',
          );
        }
      });
    return () => controller.abort();
  }, [resourceId, resource?.currentCommit, revision, version?.commit]);
  const reference: Reference | null =
    resource && path
      ? {
          ...(resource.source ?? {}),
          kind: 'file',
          resourceId: resource.id,
          path,
          commit,
          title: path.split('/').at(-1) ?? path,
        }
      : null;

  async function selectResource(id: string) {
    if (!(await flushPrivateDrafts())) return;
    invalidateEditorRequest();
    closeSources();
    setRequestReview(null);
    setImportOpen(false);
    setEnvironmentOpen(false);
    setVersion(null);
    setComparison(null);
    if (id === resourceId) {
      setEditor(null);
      setHistory(false);
      setRevision((value) => value + 1);
      return;
    }
    setResourceId(id);
    const owner = rooms.find((room) => room.resourceId === id);
    if (owner) setTargetRoom(owner.id);
    setEditor(null);
    setPath('');
    setFiles([]);
    setCommit('');
    setPublications([]);
    setError('');
    setFilesError('');
    setHistory(false);
  }

  async function edit(newFile = false) {
    invalidateEditorRequest();
    const request = editorRequest.current;
    if (!(await flushPrivateDrafts())) return;
    if (request !== editorRequest.current) return;
    if (!resource || resource.immutable || version) return;
    const baseline = commit || resource.currentCommit;
    let file = path;
    if (newFile) {
      const result = await desktop.dialog({
        title: 'New Document',
        input: { label: 'File path', value: 'memo.md' },
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Create Draft', value: 'create', default: true },
        ],
      });
      if (request !== editorRequest.current) return;
      if (result?.button !== 'create' || !result.value.trim()) return;
      file = result.value.trim();
    }
    setPending(true);
    setError('');
    const controller = new AbortController();
    editorController.current = controller;
    try {
      const content = await readTextFile(
        resource.id,
        file,
        baseline,
        newFile,
        controller.signal,
      );
      if (request !== editorRequest.current) return;
      setEnvironmentOpen(false);
      setHistory(false);
      setEditor({
        resourceId: resource.id,
        path: file,
        content,
        baseCommit: baseline,
      });
    } catch (error) {
      if (request !== editorRequest.current) return;
      setError(
        error instanceof Error ? error.message : 'Could not open the editor.',
      );
    } finally {
      if (request === editorRequest.current) {
        editorController.current = null;
        setPending(false);
      }
    }
  }

  async function copy() {
    if (!reference?.resourceId || !reference.path || !reference.commit) return;
    const room = rooms.find((item) => item.id === targetRoom);
    const target = resources.find((item) => item.id === room?.resourceId);
    if (!target) return;
    const result = await desktop.dialog({
      title: 'Create Working Copy',
      message: `Copy this exact source into ${room?.name}. The original stays unchanged.`,
      input: { label: 'File path', value: reference.path.split('/').at(-1) },
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Create Copy', value: 'copy', default: true },
      ],
    });
    if (result?.button !== 'copy') return;
    setPending(true);
    setError('');
    try {
      await libraryApi.copy(target.id, {
        requestId: crypto.randomUUID(),
        expectedCommit: target.currentCommit,
        path: result.value.trim(),
        sourceResourceId: reference.resourceId,
        sourceCommit: reference.commit,
        sourcePath: reference.path,
      });
      await selectResource(target.id);
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'The working copy could not be saved.',
      );
    } finally {
      setPending(false);
    }
  }

  async function restore(publication: Publication) {
    if (!resource) return;
    const answer = await desktop.dialog({
      title: 'Restore Shared Files',
      message:
        'Restore the complete file version shown here. This creates a new shared version; the current version stays in history.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Restore Version', value: 'restore', default: true },
      ],
    });
    if (answer?.button !== 'restore') return;
    setPending(true);
    setError('');
    try {
      await libraryApi.restore(
        resource.id,
        resource.currentCommit,
        publication.commit,
        crypto.randomUUID(),
      );
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not restore this version.',
      );
    } finally {
      setPending(false);
    }
  }

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
                  <button
                    key={item.key}
                    onClick={() =>
                      void flushPrivateDrafts().then((saved) => {
                        if (!saved) return;
                        const resource = resources.find(
                          (resource) => resource.id === item.resourceId,
                        );
                        if (!resource) return;
                        invalidateEditorRequest();
                        closeSources();
                        setEnvironmentOpen(false);
                        setRequestReview(null);
                        setVersion(null);
                        setComparison(null);
                        setResourceId(resource.id);
                        setHistory(false);
                        setEditor({
                          resourceId: resource.id,
                          path: item.path,
                          content: '',
                          baseCommit: resource.currentCommit,
                        });
                      })
                    }
                  >
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
            <Button
              ref={importLauncher}
              onClick={() =>
                void flushPrivateDrafts().then((saved) => {
                  if (!saved) return;
                  invalidateEditorRequest();
                  setImportOpen(true);
                  setRequestReview(null);
                  setEditor(null);
                  setHistory(false);
                  setEnvironmentOpen(false);
                  closeSources();
                })
              }
            >
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
                    onClick={() =>
                      void flushPrivateDrafts().then((saved) => {
                        if (saved) {
                          invalidateEditorRequest();
                          setEnvironmentOpen(false);
                          setHistory(!history);
                          setComparison(null);
                          setEditor(null);
                        }
                      })
                    }
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
                onClose={() =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) setRequestReview(null);
                  })
                }
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
                onClose={() =>
                  void flushPrivateDrafts().then((saved) => {
                    if (saved) {
                      setEditor(null);
                      setRevision((value) => value + 1);
                    }
                  })
                }
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
                      onClick={() =>
                        void flushPrivateDrafts().then((saved) => {
                          if (saved) {
                            invalidateEditorRequest();
                            setPath(file);
                            setRequestReview(null);
                          }
                        })
                      }
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
