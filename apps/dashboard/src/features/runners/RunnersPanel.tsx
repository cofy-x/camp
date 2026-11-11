import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  PixelIcon,
  Select,
  Tabs,
  type DesktopAPI,
} from '@cofy-x/camp-platinum';
import { runnersApi, type ExecutionDefaults, type Runner } from './api';
import { runnerStatus } from './presentation';
import { useRunners } from './useRunners';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { RunnerReadiness } from './RunnerReadiness';
import { RunnerPermissions } from './RunnerPermissions';
import { RunnerDiagnostics } from './RunnerDiagnostics';
import { ConnectRunner } from './ConnectRunner';
import { useRead } from '../../shared/reads/useRead';

export function RunnersPanel({
  desktop,
  windowId,
}: {
  desktop: DesktopAPI;
  windowId: string;
}) {
  const {
    runners,
    loading,
    error: loadError,
    refresh: refreshRunners,
  } = useRunners();
  const { user } = useAccount();
  const { members, navigation } = useTeam();
  const handledNavigation = useRef(0);
  const [selectedId, setSelectedId] = useState('');
  const [tab, setTab] = useState('overview');
  const [connecting, setConnecting] = useState(false);
  const defaultsRead = useRead<ExecutionDefaults>('/execution-defaults');
  const { refresh: refreshDefaults } = defaultsRead;
  const defaults = defaultsRead.data ?? null;
  const defaultsError = defaultsRead.error;
  const refresh = useCallback(() => {
    refreshRunners();
    void refreshDefaults();
  }, [refreshRunners, refreshDefaults]);
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  useEffect(() => {
    if (
      navigation?.app !== 'runners' ||
      handledNavigation.current === navigation.revision
    )
      return;
    if (navigation.runnerId && loading) return;
    handledNavigation.current = navigation.revision;
    setConnecting(Boolean(navigation.connect));
    if (navigation.runnerId) {
      if (runners.some((runner) => runner.id === navigation.runnerId)) {
        setSelectedId(navigation.runnerId);
        setTab('overview');
        setError('');
      } else
        setError(
          'This Runner is no longer available. Choose another Runner or connect a machine.',
        );
    }
  }, [navigation, loading, runners]);
  const selected =
    runners.find((runner) => runner.id === selectedId) ?? runners[0];
  const location = connecting ? 'Connect Runner' : selected?.name;
  useEffect(() => {
    desktop.setTitle(windowId, location ? `${location} — Runners` : 'Runners');
  }, [desktop, windowId, location]);
  const manage =
    selected && (user.role === 'admin' || selected.ownerUserId === user.id);
  const owner = (runner: Runner) =>
    runner.ownerUserId === user.id
      ? 'You'
      : runner.ownerUserId
        ? (members.find((member) => member.id === runner.ownerUserId)?.name ??
          'Team member')
        : 'Team owned';
  const markers = (runner: Runner) =>
    [
      runner.ownerUserId === user.id ? 'Yours' : owner(runner),
      defaults?.personalRunnerId === runner.id ? 'Your default' : '',
      defaults?.teamRunnerId === runner.id ? 'Team fallback' : '',
    ]
      .filter(Boolean)
      .join(' · ');

  function select(id: string) {
    setSelectedId(id);
    setTab('overview');
    setError('');
    setNotice('');
  }

  async function update(
    runner: Runner,
    action: 'rename' | 'toggle' | 'revoke',
  ) {
    let input: Parameters<typeof runnersApi.update>[1];
    if (action === 'rename') {
      const answer = await desktop.dialog({
        title: 'Rename Runner',
        input: { label: 'Runner name', value: runner.name },
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Rename', value: 'save', default: true },
        ],
      });
      if (answer?.button !== 'save' || !answer.value.trim()) return;
      input = { name: answer.value.trim() };
    } else if (action === 'revoke') {
      const answer = await desktop.dialog({
        title: 'Revoke Runner Access',
        message: `Disconnect ${runner.name} and stop accepting its results. Connect it again with a new connection code and a new private data directory to work in Camp.`,
        buttons: [
          { label: 'Keep Access', value: 'cancel', cancel: true },
          { label: 'Revoke Access', value: 'revoke', default: true },
        ],
      });
      if (answer?.button !== 'revoke') return;
      input = { revoke: true };
    } else {
      if (runner.enabled) {
        const answer = await desktop.dialog({
          title: 'Disable Runner',
          message: `Stop active and queued work on ${runner.name}. New requests require an enabled Runner.`,
          buttons: [
            { label: 'Keep Enabled', value: 'cancel', cancel: true },
            { label: 'Disable Runner', value: 'disable', default: true },
          ],
        });
        if (answer?.button !== 'disable') return;
      }
      input = { enabled: !runner.enabled };
    }
    setPending(runner.id);
    setError('');
    setNotice('');
    try {
      await runnersApi.update(runner.id, input);
      refresh();
      setNotice(
        action === 'revoke' ? 'Runner access revoked.' : 'Runner updated.',
      );
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Runner was not updated.',
      );
    } finally {
      setPending('');
    }
  }

  function openActions(button: HTMLButtonElement) {
    if (!selected || !manage || selected.revoked) return;
    const runner = selected;
    const rect = button.getBoundingClientRect();
    desktop.contextMenu(
      [
        { label: 'Rename…', action: () => void update(runner, 'rename') },
        {
          label: runner.enabled ? 'Disable…' : 'Enable',
          action: () => void update(runner, 'toggle'),
        },
        { separator: true },
        { label: 'Revoke…', action: () => void update(runner, 'revoke') },
      ],
      { x: rect.left, y: rect.bottom },
    );
  }

  return (
    <>
      <div className="app-toolbar camp-runners-toolbar">
        {connecting ? (
          <Button onClick={() => setConnecting(false)}>Back to Runners</Button>
        ) : (
          <Button primary onClick={() => setConnecting(true)}>
            Connect Runner…
          </Button>
        )}
        <Button disabled={Boolean(pending)} onClick={refresh}>
          Refresh
        </Button>
      </div>
      <div className="camp-runners-connect-view" hidden={!connecting}>
        <ConnectRunner
          onConnected={(id) => {
            select(id);
            setConnecting(false);
            refresh();
          }}
        />
      </div>
      <div className="camp-runners-main" hidden={connecting}>
        {runners.length > 0 && (
          <div className="camp-runners-chooser">
            <Select
              label="Runner"
              value={selected?.id ?? ''}
              disabled={Boolean(pending)}
              onChange={(event) => select(event.target.value)}
            >
              {runners.map((runner) => (
                <option key={runner.id} value={runner.id}>
                  {runner.name}
                </option>
              ))}
            </Select>
          </div>
        )}
        {loadError && (
          <p className="app-error camp-runners-error" role="alert">
            {loadError}
          </p>
        )}
        <div className="camp-runners-body">
          {runners.length > 0 && (
            <nav
              className="camp-resource-nav app-control-nav camp-runner-list"
              aria-label="Runners"
            >
              {runners.map((runner) => (
                <button
                  key={runner.id}
                  type="button"
                  aria-label={runner.name}
                  disabled={Boolean(pending)}
                  className={runner.id === selected?.id ? 'selected' : ''}
                  aria-current={runner.id === selected?.id ? 'page' : undefined}
                  onClick={() => select(runner.id)}
                >
                  <PixelIcon name="computer" size={28} />
                  <span>
                    <strong>{runner.name}</strong>
                    <small>
                      {runnerStatus(runner)}
                      {runner.runtime === 'fixture' ||
                      runner.capabilities.executor === 'fixture'
                        ? ' · Test executor'
                        : ''}
                    </small>
                    <small>{markers(runner)}</small>
                  </span>
                </button>
              ))}
            </nav>
          )}
          {selected ? (
            <section
              className="camp-runner-detail"
              aria-label="Runner details"
              key={`${user.id}:${selected.id}`}
            >
              <header className="camp-runner-heading">
                <p>
                  {runnerStatus(selected)} · {owner(selected)}
                </p>
                {manage && !selected.revoked && (
                  <Button
                    aria-label="Runner actions"
                    aria-haspopup="menu"
                    disabled={Boolean(pending)}
                    onClick={(event) => openActions(event.currentTarget)}
                    onKeyDown={(event) => {
                      if (event.key === 'ArrowDown') {
                        event.preventDefault();
                        openActions(event.currentTarget);
                      }
                    }}
                  >
                    More ▾
                  </Button>
                )}
              </header>
              {error && (
                <p className="app-error" role="alert">
                  {error}
                </p>
              )}
              {notice && <p role="status">{notice}</p>}
              <Tabs
                label="Runner details"
                value={tab}
                onChange={setTab}
                tabs={[
                  { id: 'overview', label: 'Overview' },
                  { id: 'access', label: 'Access' },
                  { id: 'diagnostics', label: 'Diagnostics' },
                ]}
              >
                <div hidden={tab !== 'overview'}>
                  <dl className="camp-runner-facts camp-runner-overview-facts">
                    <dt>Execution</dt>
                    <dd>
                      {selected.runtime === 'fixture' ||
                      selected.capabilities.executor === 'fixture'
                        ? 'Test executor'
                        : selected.capabilities.ready
                          ? selected.online
                            ? 'Ready'
                            : 'Last check passed'
                          : 'Needs attention'}
                    </dd>
                    <dt>Model</dt>
                    <dd>
                      {selected.modelConfigured
                        ? 'Configured on Server'
                        : 'Not configured'}
                    </dd>
                    <dt>Access</dt>
                    <dd>
                      {selected.revoked
                        ? 'Revoked'
                        : selected.teamShared
                          ? 'All Agents'
                          : 'Owner and allowed Agents'}
                    </dd>
                    {defaults?.personalRunnerId === selected.id && (
                      <>
                        <dt>Your default</dt>
                        <dd>For your new Agents</dd>
                      </>
                    )}
                    {defaults?.teamRunnerId === selected.id && (
                      <>
                        <dt>Team fallback</dt>
                        <dd>When no Agent or Room Runner is set</dd>
                      </>
                    )}
                  </dl>
                  {selected.revoked ? (
                    <div className="camp-runner-next-step">
                      <p>
                        This Runner can no longer accept work or return results.
                      </p>
                      {manage && (
                        <Button onClick={() => setConnecting(true)}>
                          Connect Runner…
                        </Button>
                      )}
                    </div>
                  ) : !selected.enabled ? (
                    <div className="camp-runner-next-step">
                      <p>
                        This Runner is disabled. Enable it before assigning new
                        work.
                      </p>
                      {manage && (
                        <Button
                          disabled={Boolean(pending)}
                          onClick={() => void update(selected, 'toggle')}
                        >
                          Enable Runner
                        </Button>
                      )}
                    </div>
                  ) : null}
                  <RunnerReadiness
                    runner={selected}
                    active={!connecting && tab === 'overview'}
                    getActivationRevision={desktop.getActivationRevision}
                  />
                  {(selected.runtime === 'fixture' ||
                    selected.capabilities.executor === 'fixture') && (
                    <p>
                      This test Runner verifies Camp workflows without executing
                      native Agent work.
                    </p>
                  )}
                  {!selected.runtime && !selected.capabilities.executor && (
                    <p>
                      Waiting for the Runner to report its execution
                      capabilities.
                    </p>
                  )}
                </div>
                {tab === 'access' && (
                  <>
                    {defaultsError && (
                      <p className="app-error" role="alert">
                        {defaultsError}{' '}
                        <Button onClick={refresh}>Refresh Defaults</Button>
                      </p>
                    )}
                    <RunnerPermissions
                      runner={selected}
                      defaults={defaults}
                      onSaved={refresh}
                    />
                  </>
                )}
                {tab === 'diagnostics' && (
                  <RunnerDiagnostics
                    runner={selected}
                    getActivationRevision={desktop.getActivationRevision}
                  />
                )}
              </Tabs>
            </section>
          ) : (
            <div className="camp-runner-empty">
              <div className="camp-empty" role="status">
                <PixelIcon name="computer" size={32} />
                <h2>
                  {loading
                    ? 'Loading Runners…'
                    : loadError
                      ? 'Runners unavailable'
                      : 'No Runners connected'}
                </h2>
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  );
}
