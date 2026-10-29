import { useEffect, useRef, useState } from 'react';
import { Button, PixelIcon, Select } from '@cofy-x/camp-platinum';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { APIError } from '../../shared/api/http';
import {
  runnersApi,
  type RunnerSource,
  type SourceImport,
} from '../runners/api';
import {
  importPending,
  importStatus,
  mergeImportStatus,
  runnerStatus,
} from '../runners/presentation';
import { useRunners } from '../runners/useRunners';
import {
  readImportReceipt,
  saveImportReceipt,
  type ImportReceipt,
} from './sourceImportState';

export function RunnerImport({
  onClose,
}: {
  onClose: (restoreFocus?: boolean) => void;
}) {
  const { user } = useAccount();
  const team = useTeam();
  const { runners, loading, error: runnerError, refresh } = useRunners();
  const [receipt, setReceipt] = useState<ImportReceipt | null>(() =>
    readImportReceipt(user.id),
  );
  const [runnerId, setRunnerId] = useState(receipt?.input?.runnerId ?? '');
  const [sourceId, setSourceId] = useState(receipt?.input?.sourceId ?? '');
  const [roomName, setRoomName] = useState(receipt?.input?.roomName ?? '');
  const [imports, setImports] = useState<SourceImport[]>([]);
  const [historyError, setHistoryError] = useState('');
  const [sources, setSources] = useState<{
    runnerId: string;
    items: RunnerSource[];
  } | null>(null);
  const [sourceError, setSourceError] = useState('');
  const [sourceRevision, setSourceRevision] = useState(0);
  const [value, setValue] = useState<SourceImport | null>(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [navigation, setNavigation] = useState(0);
  const announced = useRef('');
  const heading = useRef<HTMLHeadingElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const source =
    sources?.runnerId === runnerId
      ? sources.items.find((item) => item.id === sourceId)
      : undefined;
  const runner = runners.find((item) => item.id === runnerId);
  const importRunners = runners.filter(
    (item) =>
      item.enabled &&
      !item.revoked &&
      (user.role === 'admin' || item.ownerUserId === user.id),
  );
  const active = Boolean(receipt && (!value || importPending(value)));
  const recentImports = imports.filter((item) => item.id !== value?.id);

  useEffect(() => {
    content.current?.scrollTo({ top: 0 });
    heading.current?.focus({ preventScroll: true });
  }, [navigation]);

  function remember(record: ImportReceipt | null) {
    saveImportReceipt(user.id, record);
    setReceipt(record);
  }

  useEffect(() => {
    if (active) return;
    const controller = new AbortController();
    void runnersApi
      .sourceImports(controller.signal)
      .then(({ sourceImports }) => {
        if (controller.signal.aborted) return;
        setImports(sourceImports ?? []);
        setHistoryError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setHistoryError(
            error instanceof Error
              ? error.message
              : 'Could not load import history.',
          );
      });
    return () => controller.abort();
  }, [active, team.revision]);

  useEffect(() => {
    if (!runnerId || receipt) return;
    const controller = new AbortController();
    void runnersApi
      .sources(runnerId, controller.signal)
      .then(({ sources }) => {
        if (controller.signal.aborted) return;
        setSources({ runnerId, items: sources ?? [] });
        setSourceError('');
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setSourceError(
            error instanceof Error
              ? error.message
              : 'Could not load Runner sources.',
          );
      });
    return () => controller.abort();
  }, [runnerId, receipt, sourceRevision]);

  const importId = receipt?.importId;
  const terminal = value !== null && !importPending(value);
  useEffect(() => {
    if (!importId || terminal) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      try {
        const result = await runnersApi.sourceImport(
          importId!,
          controller.signal,
        );
        if (controller.signal.aborted) return;
        setValue((current) => mergeImportStatus(current, result.sourceImport));
        setError('');
        if (!importPending(result.sourceImport)) return;
      } catch (error) {
        if (controller.signal.aborted) return;
        setError(
          error instanceof Error
            ? error.message
            : 'Could not check this import.',
        );
      }
      if (!controller.signal.aborted)
        timer = setTimeout(() => void poll(), 1500);
    }

    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [importId, terminal]);

  useEffect(() => {
    if (value?.status === 'succeeded' && announced.current !== value.id) {
      announced.current = value.id;
      refresh();
    }
  }, [value, refresh]);

  async function start() {
    if (receipt?.importId) return;
    if (
      !receipt &&
      (!source ||
        !source.available ||
        !runner?.enabled ||
        runner.revoked ||
        !roomName.trim())
    )
      return;
    const record = receipt ?? {
      input: {
        requestId: crypto.randomUUID(),
        runnerId,
        sourceId,
        expectedSourceRevision: source!.revision,
        roomName: roomName.trim(),
      },
    };
    if (!record.input) return;
    remember(record);
    setPending(true);
    setError('');
    try {
      const result = await runnersApi.importSource(record.input);
      remember({ ...record, importId: result.sourceImport.id });
      setValue((current) => mergeImportStatus(current, result.sourceImport));
    } catch (error) {
      if (
        error instanceof APIError &&
        [
          'invalid_import',
          'revision_conflict',
          'source_unavailable',
          'runner_unavailable',
        ].includes(error.code)
      ) {
        remember(null);
        setSources(null);
        setSourceRevision((current) => current + 1);
        refresh();
      }
      setError(
        error instanceof Error
          ? error.message
          : 'Could not confirm the import request.',
      );
    } finally {
      setPending(false);
    }
  }

  async function cancel() {
    if (!receipt?.importId) return;
    setPending(true);
    setError('');
    try {
      const result = await runnersApi.cancelImport(receipt.importId);
      setValue((current) => mergeImportStatus(current, result.sourceImport));
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not cancel this import.',
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="camp-runner-import" aria-label="Import from Runner">
      <header className="app-toolbar camp-import-heading">
        <PixelIcon name="folder" size={24} />
        <h2 ref={heading} tabIndex={-1}>
          Import from Runner
        </h2>
        <Button onClick={() => onClose(true)}>Back to Files</Button>
      </header>
      <div className="camp-import-content" ref={content}>
        <div className="camp-import-page">
          {runnerError && (
            <p className="app-error" role="alert">
              {runnerError} <Button onClick={refresh}>Retry Runners</Button>
            </p>
          )}
          {!receipt ? (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void start();
              }}
            >
              <p className="camp-import-intro">
                Bring a directory into a new Room to work on with your team.
              </p>
              <fieldset className="app-control-group" disabled={pending}>
                <legend>Directory snapshot</legend>
                <div className="camp-import-source-fields">
                  <Select
                    label="Source Runner"
                    value={runnerId}
                    disabled={loading}
                    onChange={(event) => {
                      const next = event.target.value;
                      if (next === runnerId) return;
                      setRunnerId(next);
                      setSourceId('');
                      setSources(null);
                      setSourceError('');
                    }}
                  >
                    <option value="" disabled>
                      Choose Runner
                    </option>
                    {importRunners.map((runner) => (
                      <option key={runner.id} value={runner.id}>
                        {runner.name} · {runnerStatus(runner)}
                      </option>
                    ))}
                  </Select>
                  {runnerId && (
                    <Select
                      label="Registered directory"
                      value={sourceId}
                      disabled={sources?.runnerId !== runnerId}
                      onChange={(event) => setSourceId(event.target.value)}
                    >
                      <option value="" disabled>
                        Choose Source
                      </option>
                      {(sources?.runnerId === runnerId
                        ? sources.items
                        : []
                      ).map((source) => (
                        <option
                          key={source.id}
                          value={source.id}
                          disabled={!source.available}
                        >
                          {source.alias}
                          {source.available ? '' : ' · Unavailable'}
                        </option>
                      ))}
                    </Select>
                  )}
                </div>
                {!loading && !runnerError && !importRunners.length && (
                  <div className="camp-import-next">
                    <p>
                      Connect a Runner to import your directories, or ask
                      another Runner's owner to import a snapshot.
                    </p>
                    <Button
                      type="button"
                      onClick={() => void team.navigate({ app: 'runners' })}
                    >
                      Open Runners
                    </Button>
                  </div>
                )}
                {runnerId && !sourceError && sources?.runnerId !== runnerId && (
                  <p role="status">Loading registered sources…</p>
                )}
                {sources?.runnerId === runnerId && !sources.items.length && (
                  <p>
                    No sources are registered on this Runner. Its operator can
                    add a directory alias in the Runner configuration.
                  </p>
                )}
                {sourceError && (
                  <p className="app-error" role="alert">
                    {sourceError}{' '}
                    <Button
                      onClick={() => setSourceRevision((value) => value + 1)}
                    >
                      Retry Sources
                    </Button>
                  </p>
                )}
                <label>
                  New Room name
                  <input
                    className="p9-field"
                    required
                    maxLength={120}
                    value={roomName}
                    onChange={(event) => setRoomName(event.target.value)}
                  />
                </label>
                <p className="camp-import-consequence">
                  Agent changes are saved to Camp; the original directory stays
                  unchanged.
                </p>
                {runner && !runner.online && (
                  <p>This snapshot will wait until the Runner reconnects.</p>
                )}
                <div className="camp-import-actions">
                  <Button
                    type="submit"
                    primary
                    disabled={
                      !source?.available ||
                      !roomName.trim() ||
                      !runner?.enabled ||
                      runner.revoked
                    }
                  >
                    Import Snapshot
                  </Button>
                </div>
              </fieldset>
            </form>
          ) : (
            <div className="camp-import-progress">
              <div className="camp-import-result-heading">
                <PixelIcon
                  name={value?.status === 'succeeded' ? 'room' : 'folder'}
                  size={32}
                />
                <div>
                  <p className="camp-import-status" role="status">
                    {value
                      ? importStatus[value.status]
                      : pending
                        ? 'Submitting import…'
                        : 'Import receipt not confirmed'}
                  </p>
                  <h3>
                    {value?.roomName ||
                      receipt.input?.roomName ||
                      'Directory import'}
                  </h3>
                </div>
              </div>
              <dl className="camp-import-facts">
                <dt>Runner</dt>
                <dd>
                  {runner?.name ||
                    value?.runnerId ||
                    receipt.input?.runnerId ||
                    'Loading…'}
                </dd>
                {(value?.sourceAlias || source?.alias) && (
                  <>
                    <dt>Directory</dt>
                    <dd>{value?.sourceAlias || source?.alias}</dd>
                  </>
                )}
              </dl>
              {value?.error && (
                <p className="app-error" role="alert">
                  {value.error}
                </p>
              )}
              {!receipt.importId && (
                <div className="camp-import-next">
                  <p>
                    Retry retrieves the same request and cannot create a second
                    Room.
                  </p>
                  <div className="camp-import-actions">
                    <Button disabled={pending} onClick={() => void start()}>
                      Check Import Receipt
                    </Button>
                  </div>
                </div>
              )}
              {active && receipt.importId && (
                <div className="camp-import-next">
                  <p>
                    {value?.status === 'cancelling'
                      ? 'Waiting for the Runner to stop and clean up.'
                      : value?.status === 'queued' && runner && !runner.online
                        ? 'This import will start when the Runner reconnects.'
                        : 'You can leave this view and return to follow the import.'}
                  </p>
                  <div className="camp-import-actions">
                    <Button
                      disabled={pending || value?.status === 'cancelling'}
                      onClick={() => void cancel()}
                    >
                      {pending || value?.status === 'cancelling'
                        ? 'Stopping…'
                        : 'Cancel Import'}
                    </Button>
                  </div>
                </div>
              )}
              {value?.status === 'succeeded' && value.roomId && (
                <div className="camp-import-next">
                  <p>
                    Open the Room, choose an Agent, and describe the work to do
                    with these files.
                  </p>
                  <div className="camp-import-actions">
                    <Button
                      primary
                      onClick={() =>
                        void team.navigate({ roomId: value.roomId! })
                      }
                    >
                      Open Room
                    </Button>
                    {value.resourceId && (
                      <Button
                        onClick={() =>
                          void team
                            .navigate({
                              app: 'library',
                              resourceId: value.resourceId,
                              contextRoomId: value.roomId,
                            })
                            .then((opened) => {
                              if (opened) onClose();
                            })
                        }
                      >
                        Open Imported Files
                      </Button>
                    )}
                  </div>
                </div>
              )}
              {terminal && (
                <div className="camp-import-another">
                  <Button
                    onClick={() => {
                      remember(null);
                      setValue(null);
                      setError('');
                      setNavigation((current) => current + 1);
                    }}
                  >
                    Import Another Snapshot
                  </Button>
                </div>
              )}
            </div>
          )}
          {error && (
            <p className="app-error" role="alert">
              {error}
              {active
                ? ' The import may still be active; its receipt is preserved.'
                : ''}
            </p>
          )}
          {!active && (recentImports.length > 0 || historyError) && (
            <section
              className="camp-import-history"
              aria-label="Recent imports"
            >
              {historyError && (
                <p className="app-error" role="alert">
                  {historyError}{' '}
                  <Button onClick={refresh}>Retry History</Button>
                </p>
              )}
              {recentImports.length > 0 && (
                <details>
                  <summary>Recent imports</summary>
                  <ul>
                    {recentImports.slice(0, 20).map((item) => (
                      <li key={item.id}>
                        <div>
                          <strong>{item.roomName}</strong>
                          <span>
                            {importStatus[item.status]} · {item.sourceAlias}
                          </span>
                        </div>
                        <Button
                          onClick={() => {
                            remember({ importId: item.id });
                            setValue(item);
                            setRunnerId(item.runnerId);
                            setSourceId(item.sourceId);
                            setRoomName(item.roomName);
                            setSources(null);
                            setSourceError('');
                            setError('');
                            setNavigation((current) => current + 1);
                          }}
                        >
                          View Import
                        </Button>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
            </section>
          )}
        </div>
      </div>
    </section>
  );
}
