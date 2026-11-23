import {
  useCallback,
  useEffect,
  useEffectEvent,
  useRef,
  useState,
} from 'react';
import {
  Button,
  PixelIcon,
  StatusBar,
  Select,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';
import {
  workspaceApi,
  type Agent,
  type Room,
  type WorkspaceStatus,
} from '../workspace/api';
import { AgentEditor, agentEditorBlock } from '../workspace/AgentsPanel';
import { IdentityAvatar } from '../identity/IdentityAvatar';
import { useAccount } from '../../team/AuthContext';
import { useTeam } from '../../team/TeamContext';
import { useRead } from '../../shared/reads/useRead';

export function Agents({ desktop, window }: AppWindowProps) {
  const { user } = useAccount();
  const team = useTeam();
  const agentsRead = useRead<{ agents: Agent[] }>('/agents');
  const roomsRead = useRead<{ rooms: Room[] }>('/rooms');
  const statusRead = useRead<WorkspaceStatus>('/workspace/status');
  const agents = agentsRead.data?.agents ?? [];
  const rooms = roomsRead.data?.rooms ?? [];
  const status = statusRead.data ?? null;
  const [selection, setSelected] = useState('');
  const selected = selection || agents[0]?.id || '';
  const membershipsRead = useRead<{ roomIds: string[] }>(
    selected ? `/agents/${encodeURIComponent(selected)}/rooms` : null,
  );
  const joined = new Set(membershipsRead.data?.roomIds ?? []);
  const membershipsReady = Boolean(membershipsRead.data);
  const [editor, setEditor] = useState<Agent | 'new' | null>(null);
  const creating = editor === 'new';
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const dirty = useRef(false);
  const busy = useRef(false);
  const updateDirty = useCallback((value: boolean) => {
    dirty.current = value;
  }, []);
  const updatePending = useCallback((value: boolean) => {
    busy.current = value;
    setPending(value);
    if (value) setError('');
  }, []);
  const canLeave = useCallback(() => {
    const warning = agentEditorBlock(busy.current, dirty.current);
    if (!warning) return true;
    setError(warning);
    return false;
  }, []);
  const { beforeClose, setTitle } = desktop;
  useEffect(
    () => beforeClose(window.id, canLeave),
    [beforeClose, window.id, canLeave],
  );

  function closeEditor() {
    dirty.current = false;
    setEditor(null);
    setError('');
  }

  function selectAgent(id: string) {
    if ((id === selected && !creating) || !canLeave()) return;
    setSelected(id);
    closeEditor();
  }

  const selectFromNavigation = useEffectEvent(selectAgent);
  const readError =
    agentsRead.error ||
    roomsRead.error ||
    statusRead.error ||
    membershipsRead.error;
  const bodyError = editor ? readError : error || readError;
  useEffect(() => {
    if (team.navigation?.app === 'agents' && team.navigation.agentId)
      selectFromNavigation(team.navigation.agentId);
  }, [team.navigation]);
  const agent = agents.find((item) => item.id === selected);
  const location = creating
    ? 'New Agent'
    : editor
      ? `Edit ${editor.name}`
      : agent?.name;
  useEffect(() => {
    setTitle(window.id, location ? `${location} — Agents` : 'Agents');
  }, [setTitle, window.id, location]);
  const canEdit = user.role === 'admin' || agent?.ownerUserId === user.id;

  async function membership(room: Room) {
    if (!agent || busy.current) return;
    if (joined.has(room.id)) {
      const answer = await desktop.dialog({
        title: 'Leave Room',
        message: `Remove ${agent.name} from ${room.name}? Queued and active requests in that room will stop. History stays available.`,
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Leave Room', value: 'leave', default: true },
        ],
      });
      if (answer?.button !== 'leave') return;
    }
    updatePending(true);
    try {
      await workspaceApi.membership(room.id, agent.id, !joined.has(room.id));
      team.refresh();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : 'Could not update membership.',
      );
    } finally {
      updatePending(false);
    }
  }

  return (
    <div className="camp-agents-app">
      <div className="app-toolbar">
        {(agents.length > 0 || creating || selected) && (
          <div className="camp-agents-chooser">
            <Select
              label="Agent"
              value={creating ? '' : selected}
              disabled={pending}
              onChange={(event) => selectAgent(event.target.value)}
            >
              {creating && <option value="">New Agent</option>}
              {!creating && selected && !agent && (
                <option value={selected}>Selected Agent</option>
              )}
              {agents.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </Select>
          </div>
        )}
        <Button
          disabled={pending}
          onClick={() => {
            if (creating || !canLeave()) return;
            setError('');
            setEditor('new');
          }}
        >
          New Agent…
        </Button>
      </div>
      <div className="camp-agents-body">
        {agents.length > 0 && (
          <nav
            className="camp-resource-nav app-control-nav"
            aria-label="Agents"
          >
            {agents.map((item) => (
              <button
                key={item.id}
                className={item.id === selected && !creating ? 'selected' : ''}
                aria-current={
                  item.id === selected && !creating ? 'page' : undefined
                }
                disabled={pending}
                onClick={() => selectAgent(item.id)}
              >
                <IdentityAvatar avatar={item.avatar} kind="agent" size={28} />
                <span>
                  {item.name}
                  <small>@{item.handle}</small>
                </span>
              </button>
            ))}
          </nav>
        )}
        <main
          className={`camp-agent-detail${editor ? ' camp-agent-editing' : !agent ? ' camp-agent-empty' : ''}`}
        >
          {bodyError && (
            <p className="app-error" role="alert">
              {bodyError}
            </p>
          )}
          {editor ? (
            <AgentEditor
              key={editor === 'new' ? editor : editor.id}
              agent={editor === 'new' ? undefined : editor}
              agents={agents}
              showHeading={false}
              navigationError={error}
              onSave={(saved) => {
                team.refresh();
                setSelected(saved.id);
              }}
              onClose={closeEditor}
              onPending={updatePending}
              onDirtyChange={updateDirty}
            />
          ) : agent ? (
            <>
              <div className="panel-heading">
                <IdentityAvatar avatar={agent.avatar} kind="agent" />
                <div>
                  <p>
                    @{agent.handle} ·{' '}
                    {agent.ownerUserId
                      ? `Owned by ${team.members.find((member) => member.id === agent.ownerUserId)?.name ?? 'a team member'} · `
                      : 'Team owned · '}
                    {agent.enabled
                      ? agent.access === 'read'
                        ? 'Can read files'
                        : 'Can edit working files'
                      : 'Disabled'}
                  </p>
                </div>
              </div>
              {agent.instructions && (
                <fieldset className="app-control-group camp-agent-role">
                  <legend>Responsibilities</legend>
                  <p>{agent.instructions}</p>
                </fieldset>
              )}
              {canEdit && (
                <Button
                  disabled={pending}
                  onClick={() => {
                    setError('');
                    setSelected(agent.id);
                    setEditor(agent);
                  }}
                >
                  Edit Agent…
                </Button>
              )}
              <h3>Rooms</h3>
              <ul className="camp-agent-rooms">
                {rooms.map((room) => (
                  <li key={room.id}>
                    <div className="camp-agent-room-name">
                      <button
                        className="camp-link"
                        onClick={() => void team.navigate({ roomId: room.id })}
                      >
                        {room.name}
                      </button>
                      <span>
                        {!membershipsReady
                          ? 'Loading…'
                          : joined.has(room.id)
                            ? 'Member'
                            : 'Not joined'}
                      </span>
                    </div>
                    {(canEdit ||
                      (!agent.ownerUserId && !joined.has(room.id))) && (
                      <Button
                        disabled={
                          pending || !agent.enabled || !membershipsReady
                        }
                        onClick={() => void membership(room)}
                      >
                        {joined.has(room.id) ? 'Leave Room' : 'Join Room'}
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
              {status && !status.available && (
                <p className="app-error">
                  {status.reason || 'Agent work is unavailable.'}{' '}
                  {user.role === 'admin' && (
                    <Button
                      onClick={() =>
                        void team.navigate({
                          app: 'settings',
                          section: 'readiness',
                        })
                      }
                    >
                      Open Settings
                    </Button>
                  )}
                </p>
              )}
            </>
          ) : (
            <div className="camp-empty" role="status">
              <PixelIcon name="agent" size={32} />
              <h2>
                {agentsRead.loading || (selected && agentsRead.refreshing)
                  ? 'Loading Agents…'
                  : agentsRead.error || selected
                    ? 'Agents unavailable'
                    : 'No Agents yet'}
              </h2>
            </div>
          )}
        </main>
      </div>
      <StatusBar>
        <span>
          {agents.length} {agents.length === 1 ? 'Agent' : 'Agents'}
        </span>
      </StatusBar>
    </div>
  );
}
