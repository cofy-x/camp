import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { AppWindowProps, MenuItem } from '@cofy-x/camp-platinum';
import { libraryApi, type LibraryResource, type Publication } from './api';
import type { Reference, Room } from '../workspace/api';
import { useTeam } from '../../team/TeamContext';
import { useRead } from '../../shared/reads/useRead';
import { useAccount } from '../../team/AuthContext';
import { flushPrivateDrafts } from '../../team/draftRegistry';
import { teamApi } from '../../team/api';
import { readTextFile } from './textFile';

// Owns reads, mutations and recovery for this mounted view.
export function useLibraryController({ desktop, window }: AppWindowProps) {
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
  const [history, setHistory] = useState(false);
  const historyRead = useRead<{ publications: Publication[] }>(
    resourceId ? `/resources/${encodeURIComponent(resourceId)}/history` : null,
    { enabled: history },
  );
  const publications = historyRead.data?.publications ?? [];
  const historyError = historyRead.error;
  const historyLoading = history && historyRead.loading;
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
  const libraryRead =
    useRead<Awaited<ReturnType<typeof libraryApi.list>>>('/library');
  const libraryError = libraryRead.error;
  const loading = libraryRead.loading;
  const filesRead = useRead<{ files: string[]; commit: string }>(
    resourceId
      ? `/resources/${encodeURIComponent(resourceId)}/files${version ? `?commit=${encodeURIComponent(version.commit)}` : ''}`
      : null,
  );
  const filesError = filesRead.error;
  const filesLoading = filesRead.loading;
  const files = filesRead.data?.files ?? [];
  const commit = filesRead.data?.commit ?? '';
  const [pending, setPending] = useState(false);
  const [revision, setRevision] = useState(0);
  const editorRequest = useRef(0);
  const editorController = useRef<AbortController | null>(null);
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

  async function flushTransition() {
    team.cancelNavigation();
    const teamRequest = team.getNavigationRevision();
    invalidateEditorRequest();
    const request = editorRequest.current;
    const activation = desktop.getActivationRevision();
    const saved = await flushPrivateDrafts();
    return (
      saved &&
      request === editorRequest.current &&
      teamRequest === team.getNavigationRevision() &&
      activation === desktop.getActivationRevision()
    );
  }

  const openLibraryActions = (button: HTMLButtonElement) => {
    const items: MenuItem[] = [];
    const downloadCommit = version?.commit ?? commit;
    items.push({
      label: 'Download Files',
      href:
        resource && downloadCommit
          ? libraryApi.archiveURL(resource.id, downloadCommit)
          : undefined,
      download: true,
      disabled:
        !resource ||
        !downloadCommit ||
        !files.length ||
        filesLoading ||
        commit !== (version?.commit ?? resource?.currentCommit) ||
        Boolean(filesError) ||
        loading ||
        Boolean(libraryError) ||
        pending ||
        Boolean(editor) ||
        Boolean(requestReview) ||
        history ||
        Boolean(comparison) ||
        environmentOpen,
    });
    items.push({ separator: true });
    if (user.role === 'admin' && resource && !resource.immutable) {
      items.push(
        {
          label: 'Environment…',
          disabled: Boolean(requestReview),
          action: () =>
            void flushTransition().then((saved) => {
              if (!saved) return;
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
    const result = libraryRead.data;
    if (!result) return;
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
  }, [libraryRead.data]);
  useEffect(() => {
    const intent = team.navigation;
    if (
      intent?.app !== 'library' ||
      appliedNavigation.current === intent.revision
    )
      return;
    // Navigation owns interaction state; list completion must not replay it
    // over actions taken while the initial read or its retry was pending.
    appliedNavigation.current = intent.revision;
    invalidateEditorRequest();
    const context =
      rooms.find((room) => room.id === intent.contextRoomId) ??
      rooms.find((room) => room.resourceId === intent.resourceId);
    if (intent.contextRoomId) setTargetRoom(intent.contextRoomId);
    else if (context) setTargetRoom(context.id);
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
    }
    if (intent.path) setPath(intent.path);
  }, [team.navigation, rooms, invalidateEditorRequest]);
  const resource = resources.find((item) => item.id === resourceId);
  const location =
    (resource &&
      !resource.immutable &&
      rooms.find(
        (room) =>
          room.resourceId === resource.id &&
          resource.name === `${room.name} files`,
      )?.name) ||
    resource?.name;
  useEffect(() => {
    desktop.setTitle(
      window.id,
      importOpen
        ? 'Import from Runner — Library'
        : requestReview
          ? ['Request Review', location, 'Library'].filter(Boolean).join(' — ')
          : location
            ? `${location} — Library`
            : 'Library',
    );
  }, [desktop, window.id, location, importOpen, requestReview]);
  useEffect(() => {
    selectedResource.current = resourceId;
  }, [resourceId]);
  const reviewRoom =
    rooms.find(
      (item) => item.resourceId === resourceId && item.id === targetRoom,
    ) ?? rooms.find((item) => item.resourceId === resourceId);
  useEffect(() => {
    const listing = filesRead.data;
    if (!listing) return;
    setPath((current) =>
      listing.files.includes(current) ? current : (listing.files[0] ?? ''),
    );
  }, [filesRead.data]);
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
    if (!(await flushTransition())) return;
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
    setError('');
    setHistory(false);
  }

  async function edit(newFile = false) {
    team.cancelNavigation();
    const teamRequest = team.getNavigationRevision();
    invalidateEditorRequest();
    const request = editorRequest.current;
    const activation = desktop.getActivationRevision();
    if (!(await flushPrivateDrafts())) return;
    if (
      request !== editorRequest.current ||
      teamRequest !== team.getNavigationRevision() ||
      activation !== desktop.getActivationRevision()
    )
      return;
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
      if (
        request !== editorRequest.current ||
        teamRequest !== team.getNavigationRevision() ||
        activation !== desktop.getActivationRevision()
      )
        return;
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
      if (
        request !== editorRequest.current ||
        teamRequest !== team.getNavigationRevision() ||
        activation !== desktop.getActivationRevision()
      )
        return;
      setEnvironmentOpen(false);
      setHistory(false);
      setEditor({
        resourceId: resource.id,
        path: file,
        content,
        baseCommit: baseline,
      });
    } catch (error) {
      if (
        request !== editorRequest.current ||
        teamRequest !== team.getNavigationRevision() ||
        activation !== desktop.getActivationRevision()
      )
        return;
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

  function viewVersion(publication: Publication, sourceId: string) {
    if (selectedResource.current !== sourceId) return;
    setVersion({
      commit: publication.commit,
      createdAt: publication.createdAt,
    });
    setHistory(false);
    setComparison(null);
    setPath('');
  }

  function compareVersion(publication: Publication, sourceId: string) {
    if (selectedResource.current !== sourceId) return;
    setComparison(publication);
  }

  async function restore(publication: Publication, source: LibraryResource) {
    if (
      source.immutable ||
      source.currentCommit === publication.commit ||
      pending
    )
      return;
    const target = {
      id: source.id,
      name: source.name,
      expectedCommit: source.currentCommit,
      commit: publication.commit,
    };
    const answer = await desktop.dialog({
      title: 'Restore Shared Files',
      message: `Restore all shared files in “${target.name}” to this version? This creates a new shared version; the current version stays in history.`,
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
        target.id,
        target.expectedCommit,
        target.commit,
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

  async function openDraft(item: (typeof drafts)[number]) {
    if (!(await flushTransition())) return;
    const resource = resources.find(
      (resource) => resource.id === item.resourceId,
    );
    if (!resource) return;
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
  }

  async function openImport() {
    if (!(await flushTransition())) return;
    setImportOpen(true);
    setRequestReview(null);
    setEditor(null);
    setHistory(false);
    setEnvironmentOpen(false);
    closeSources();
  }

  async function toggleHistory() {
    if (!(await flushTransition())) return;
    setEnvironmentOpen(false);
    setHistory(!history);
    setComparison(null);
    setEditor(null);
  }

  async function closeReview() {
    if (await flushTransition()) setRequestReview(null);
  }

  async function closeEditor() {
    if (!(await flushTransition())) return;
    setEditor(null);
    setRevision((value) => value + 1);
  }

  async function selectFile(file: string) {
    if (!(await flushTransition())) return;
    setPath(file);
    setRequestReview(null);
  }

  return {
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
    historyLoading,
    historyError,
    retryHistory: () => void historyRead.refresh(),
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
  };
}
