import { useAccount } from '../../team/AuthContext';
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import {
  Button,
  Checkbox,
  PixelIcon,
  Select,
  type DesktopAPI,
} from '@cofy-x/camp-platinum';
import type { Agent, Room } from './api';
import { workspaceApi } from './api';
import { agentIcon } from './RoomTimeline';
import type { AgentPreset } from './agentPresets';
import { agentPresets, suggestAgentHandle } from './agentPresets';
import { useRunners } from '../runners/useRunners';
import { useTeam } from '../../team/TeamContext';
import { registerDraftWriter } from '../../team/draftRegistry';
import './agents.css';
import { IdentityAvatar } from '../identity/IdentityAvatar';
import { AvatarPicker } from '../identity/AvatarPicker';

export function agentEditorBlock(saving: boolean, dirty: boolean) {
  if (saving) return 'Wait for the Agent update to finish.';
  if (dirty) return 'Save or cancel your Agent edits before leaving.';
  return '';
}

export function AgentEditor({
  agent,
  room,
  agents,
  showHeading = true,
  navigationError,
  onSave,
  onClose,
  onPending,
  onDirtyChange,
}: {
  agent?: Agent;
  room?: Room;
  agents: Agent[];
  showHeading?: boolean;
  navigationError?: string;
  onSave: (agent: Agent) => void;
  onClose: () => void;
  onPending: (pending: boolean) => void;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const handleHelp = useId();
  const runners = useRunners();
  const [preferredRunnerId, setPreferredRunnerId] = useState(
    agent?.preferredRunnerId ?? '',
  );
  const [name, setName] = useState(agent?.name ?? '');
  const [customHandle, setCustomHandle] = useState(agent?.handle ?? null);
  const [submittedHandle, setSubmittedHandle] = useState<string | null>(null);
  const [icon, setIcon] = useState<string>(agentIcon(agent?.icon ?? 'agent'));
  const [avatar, setAvatar] = useState(agent?.avatar ?? '');
  const [choosingAvatar, setChoosingAvatar] = useState(false);
  const [instructions, setInstructions] = useState(agent?.instructions ?? '');
  const [access, setAccess] = useState<Agent['access']>(
    agent?.access ?? 'write',
  );
  const [preset, setPreset] = useState<AgentPreset['id']>('custom');
  const [enabled, setEnabled] = useState(agent?.enabled ?? true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [blocked, setBlocked] = useState('');
  const saving = useRef(false);
  const unsaved = useRef(false);
  const mounted = useRef(false);
  const roomId = room?.id;
  const handle =
    customHandle ??
    submittedHandle ??
    suggestAgentHandle(
      name,
      agents.filter((item) => item.id !== agent?.id).map((item) => item.handle),
    );
  const dirty =
    name !== (agent?.name ?? '') ||
    handle !== (agent?.handle ?? '') ||
    icon !== agentIcon(agent?.icon ?? 'agent') ||
    avatar !== (agent?.avatar ?? '') ||
    instructions !== (agent?.instructions ?? '') ||
    access !== (agent?.access ?? 'write') ||
    enabled !== (agent?.enabled ?? true) ||
    preferredRunnerId !== (agent?.preferredRunnerId ?? '');

  useLayoutEffect(() => {
    unsaved.current = dirty;
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (!roomId) return;
    return registerDraftWriter(`room-agent-editor:${handleHelp}`, async () => {
      const warning = agentEditorBlock(saving.current, unsaved.current);
      if (warning) setBlocked(warning);
      return !warning;
    });
  }, [roomId, handleHelp]);

  function choosePreset(value: AgentPreset) {
    setPreset(value.id);
    setName(value.name);
    setSubmittedHandle(null);
    setIcon(value.icon);
    setInstructions(value.instructions);
    setAccess(value.access);
  }

  async function save() {
    if (saving.current) return;
    saving.current = true;
    setSubmittedHandle(handle);
    setPending(true);
    onPending(true);
    setError('');
    setBlocked('');
    try {
      const value = {
        ...agent,
        name: name.trim(),
        handle: handle.trim(),
        icon,
        avatar,
        instructions,
        access,
        enabled,
        preferredRunnerId,
      };
      const saved =
        agent || !room
          ? await workspaceApi.saveAgent(value)
          : await workspaceApi.createRoomAgent(room.id, value);
      if (mounted.current) {
        onSave(saved.agent);
        onClose();
      }
    } catch (error) {
      if (mounted.current)
        setError(
          error instanceof Error ? error.message : 'Could not save this agent.',
        );
    } finally {
      saving.current = false;
      if (mounted.current) {
        setPending(false);
        onPending(false);
      }
    }
  }

  return (
    <form
      className="workspace-editor workspace-agent-editor"
      aria-label="Agent settings"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      {showHeading && (
        <h3>
          {agent
            ? `Edit ${agent.name}`
            : room
              ? `Add an agent to ${room.name}`
              : 'Create your Agent'}
        </h3>
      )}
      <label>
        Name
        <input
          autoFocus
          required
          disabled={pending}
          maxLength={120}
          placeholder="For example, Product Writer"
          value={name}
          onChange={(event) => {
            setName(event.target.value);
            setSubmittedHandle(null);
          }}
        />
      </label>
      <label>
        Responsibilities
        <textarea
          disabled={pending}
          rows={4}
          maxLength={8192}
          value={instructions}
          onChange={(event) => setInstructions(event.target.value)}
          placeholder="What should this agent focus on?"
        />
      </label>
      {!agent && (
        <details>
          <summary>Start with a role</summary>
          <fieldset
            className="workspace-agent-presets"
            disabled={pending}
            aria-label="Agent roles"
          >
            <div>
              {agentPresets.map((item) => (
                <Button
                  key={item.id}
                  type="button"
                  aria-label={item.label}
                  aria-pressed={preset === item.id}
                  onClick={() => choosePreset(item)}
                >
                  <PixelIcon name={agentIcon(item.icon)} size={24} />
                  <span>
                    <strong>{item.label}</strong>
                    <small>{item.description}</small>
                  </span>
                </Button>
              ))}
            </div>
          </fieldset>
        </details>
      )}
      <div className="workspace-agent-routing">
        <div className="workspace-agent-access">
          <Select
            disabled={pending}
            label="File access"
            value={access}
            onChange={(event) =>
              setAccess(event.target.value as Agent['access'])
            }
          >
            <option value="write">Can edit files</option>
            <option value="read">Read only</option>
          </Select>
          {access === 'write' && (
            <p className="workspace-description">
              Checked results from ordinary requests are saved to Room files.
            </p>
          )}
        </div>
        <div>
          <Select
            label="Preferred Runner"
            value={preferredRunnerId}
            disabled={pending || runners.loading}
            onChange={(event) => setPreferredRunnerId(event.target.value)}
          >
            <option value="">
              {agent ? 'No preferred Runner' : 'Use your default Runner'}
            </option>
            {runners.runners
              .filter((runner) => !runner.revoked)
              .map((runner) => (
                <option
                  key={runner.id}
                  value={runner.id}
                  disabled={!runner.enabled}
                >
                  {runner.name}
                  {!runner.online ? ' · Offline, work will queue' : ''}
                </option>
              ))}
          </Select>
          {runners.error && (
            <p className="app-error" role="alert">
              {runners.error}
            </p>
          )}
        </div>
      </div>
      <details
        className="camp-avatar-disclosure"
        onToggle={(event) => setChoosingAvatar(event.currentTarget.open)}
      >
        <summary>
          <span>
            <IdentityAvatar avatar={avatar} kind="agent" size={32} />
            Avatar
          </span>
        </summary>
        {choosingAvatar && (
          <AvatarPicker
            kind="agent"
            value={avatar}
            onChange={setAvatar}
            disabled={pending}
            automatic={!agent}
          />
        )}
      </details>
      <details className="workspace-agent-identity">
        <summary>
          Identity options
          {handle && <span>@{handle}</span>}
        </summary>
        <div>
          <label>
            Handle
            <input
              required
              disabled={pending}
              aria-label="Handle"
              aria-describedby={handleHelp}
              pattern="[a-z]([a-z0-9_]|-){0,47}"
              maxLength={48}
              value={handle}
              onInvalid={(event) => {
                event.currentTarget
                  .closest('details')
                  ?.setAttribute('open', '');
              }}
              onChange={(event) =>
                setCustomHandle(event.target.value.toLowerCase())
              }
            />
            <small id={handleHelp}>
              Start with a letter; use lowercase letters, numbers, hyphens or
              underscores.
            </small>
          </label>
        </div>
      </details>
      {agent && (
        <Checkbox
          disabled={pending}
          label="Enabled"
          checked={enabled}
          onChange={(event) => setEnabled(event.target.checked)}
        />
      )}
      {!enabled && (
        <p className="workspace-description">
          Disabling this Agent cancels its queued and active work in every Room.
        </p>
      )}
      <footer className="workspace-agent-editor-actions">
        {agent ? (
          <p className="workspace-description">
            Changes apply in every Room this Agent joins.
          </p>
        ) : room ? (
          <p className="workspace-description">
            Adding your Agent shares it with everyone in this Room. They can ask
            it to work using its selected Runner.
          </p>
        ) : null}
        {(error || navigationError || blocked) && (
          <p className="app-error" role="alert">
            {error || navigationError || blocked}
          </p>
        )}
        <div className="app-actions">
          <Button
            primary
            type="submit"
            disabled={pending || !name.trim() || !handle.trim()}
          >
            {pending
              ? 'Saving…'
              : agent
                ? 'Save Agent'
                : room
                  ? 'Create and Add'
                  : 'Create Agent'}
          </Button>
          <Button type="button" disabled={pending} onClick={onClose}>
            Cancel
          </Button>
        </div>
      </footer>
    </form>
  );
}

export function AgentsPanel({
  desktop,
  room,
  agents,
  members,
  onChange,
  onClose,
}: {
  desktop: DesktopAPI;
  room: Room;
  agents: Agent[];
  members: Agent[];
  onChange: () => void;
  onClose: () => void;
}) {
  const { user } = useAccount();
  const admin = user.role === 'admin';
  const team = useTeam();
  const [editing, setEditing] = useState<Agent | 'new' | null>(null);
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const joinedIds = new Set(members.map((member) => member.id));
  const groups = [
    {
      label: 'In this room',
      joined: true,
      agents: agents.filter((agent) => joinedIds.has(agent.id)),
    },
    {
      label: 'Available agents',
      joined: false,
      agents: agents.filter((agent) => !joinedIds.has(agent.id)),
    },
  ];

  async function membership(agent: Agent, joined: boolean) {
    if (joined) {
      const answer = await desktop.dialog({
        title: 'Leave Room',
        message: `Remove ${agent.name} from ${room.name}? Queued and active requests in this Room will stop. History stays available.`,
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true, default: true },
          { label: 'Leave Room', value: 'leave' },
        ],
      });
      if (answer?.button !== 'leave') return;
    }
    setPending(agent.id);
    setError('');
    setNotice('');
    try {
      await workspaceApi.membership(room.id, agent.id, !joined);
      onChange();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not update room membership.',
      );
    } finally {
      setPending('');
    }
  }

  return (
    <section
      className="workspace-panel workspace-agents"
      aria-label="Room agents"
    >
      <div className="workspace-panel-heading">
        <div className="app-actions">
          {!editing && (
            <Button
              disabled={Boolean(pending)}
              onClick={() => {
                setNotice('');
                setError('');
                setEditing('new');
              }}
            >
              New Agent…
            </Button>
          )}
          <Button disabled={Boolean(pending)} onClick={onClose}>
            Back to Chat
          </Button>
        </div>
      </div>
      {editing ? (
        <AgentEditor
          key={typeof editing === 'string' ? editing : editing.id}
          agent={editing === 'new' ? undefined : editing}
          room={room}
          agents={agents}
          onSave={(saved) => {
            onChange();
            setNotice(
              editing === 'new'
                ? `${saved.name} joined ${room.name}.`
                : `${saved.name} was updated.`,
            );
          }}
          onClose={() => setEditing(null)}
          onPending={(value) => setPending(value ? 'editor' : '')}
        />
      ) : (
        <>
          {error && (
            <p className="app-error" role="alert">
              {error}
            </p>
          )}
          {notice && (
            <p className="workspace-agent-notice" role="status">
              {notice}
            </p>
          )}
          {groups.map((group) =>
            !group.joined && group.agents.length === 0 ? null : (
              <section
                key={group.label}
                className="workspace-agent-group"
                aria-label={group.label}
              >
                <h3>
                  {group.label} <span>{group.agents.length}</span>
                </h3>
                {group.agents.length === 0 && (
                  <p className="workspace-description">
                    {admin
                      ? 'Add an available Agent or create one.'
                      : 'Add an available Agent to this Room.'}
                  </p>
                )}
                <ul className="workspace-agent-list">
                  {group.agents.map((agent) => (
                    <li key={agent.id}>
                      <IdentityAvatar avatar={agent.avatar} kind="agent" />
                      <div className="workspace-agent-copy">
                        <strong>{agent.name}</strong>
                        <small>
                          @{agent.handle} ·{' '}
                          {agent.ownerUserId
                            ? `${team.members.find((member) => member.id === agent.ownerUserId)?.name ?? 'Team member'} · `
                            : 'Team owned · '}
                          {agent.enabled
                            ? agent.access === 'write'
                              ? 'Can edit files'
                              : 'Read only'
                            : 'Disabled'}
                        </small>
                        {agent.instructions ? (
                          <details className="workspace-agent-responsibilities">
                            <summary>Responsibilities</summary>
                            <p>{agent.instructions}</p>
                          </details>
                        ) : (
                          <p>A general-purpose assistant.</p>
                        )}
                      </div>
                      <div className="workspace-agent-actions">
                        {(admin ||
                          agent.ownerUserId === user.id ||
                          (!agent.ownerUserId && !group.joined)) && (
                          <Button
                            disabled={
                              Boolean(pending) ||
                              (!group.joined && !agent.enabled)
                            }
                            onClick={() => void membership(agent, group.joined)}
                          >
                            {pending === agent.id
                              ? 'Updating…'
                              : group.joined
                                ? 'Leave Room'
                                : 'Join Room'}
                          </Button>
                        )}
                        {(admin || agent.ownerUserId === user.id) && (
                          <Button
                            disabled={Boolean(pending)}
                            onClick={() => {
                              setNotice('');
                              setError('');
                              setEditing(agent);
                            }}
                          >
                            Edit
                          </Button>
                        )}
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            ),
          )}
        </>
      )}
    </section>
  );
}
