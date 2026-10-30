import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { Button, PixelIcon } from '@cofy-x/camp-platinum';
import { teamApi, type User } from './api';
import { APIError } from '../shared/api/http';
import { flushPrivateDrafts } from './draftRegistry';
import { AuthContext } from './AuthContext';
import { uploadDrafts } from '../features/workspace/uploadState';
import { setConversationOwner } from '../features/workspace/conversationState';

export function AuthGate({ children }: { children: ReactNode }) {
  const owner = useRef('');
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [setup, setSetup] = useState(false);
  const [invite, setInvite] = useState(
    () => new URLSearchParams(location.hash.slice(1)).get('invite') ?? '',
  );
  const [username, setUsername] = useState('');
  const [name, setName] = useState('');
  const [teamName, setTeamName] = useState('Camp');
  const [password, setPassword] = useState('');
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [connectionError, setConnectionError] = useState(false);
  const [pending, setPending] = useState(false);
  const acceptUser = useCallback((next: User | null) => {
    const nextOwner = next?.id ?? 'signed-out';
    if (owner.current !== nextOwner) {
      uploadDrafts.suspend();
      owner.current = nextOwner;
    }
    setConversationOwner(nextOwner);
    setUser(next);
  }, []);
  const refresh = useCallback(async () => {
    setError('');
    setConnectionError(false);
    try {
      const state = await teamApi.status();
      setSetup(state.setupRequired);
      acceptUser(state.user);
    } catch (error) {
      setConnectionError(true);
      setError(error instanceof Error ? error.message : 'Could not open Camp.');
    } finally {
      setLoading(false);
    }
  }, [acceptUser]);
  useEffect(() => {
    void refresh();
    const expired = () => {
      acceptUser(null);
      setPassword('');
      setConnectionError(false);
      setError('Your session ended. Sign in to continue.');
    };
    window.addEventListener('camp:session-expired', expired);
    return () => window.removeEventListener('camp:session-expired', expired);
  }, [refresh, acceptUser]);

  async function signOut() {
    if (!(await flushPrivateDrafts()))
      throw new Error(
        'A private draft could not be saved. Resolve its conflict or connection error before signing out.',
      );
    await teamApi.logout();
    acceptUser(null);
    setPassword('');
  }

  async function submit() {
    setPending(true);
    setError('');
    setConnectionError(false);
    try {
      const result = await teamApi.authenticate(
        setup ? 'setup' : invite ? 'accept-invite' : 'login',
        {
          username,
          password,
          ...(setup ? { teamName } : {}),
          ...(setup || invite ? { name, token: setup ? token : invite } : {}),
        },
      );
      acceptUser(result.user);
      setPassword('');
      setToken('');
      setInvite('');
      setSetup(false);
      history.replaceState(null, '', location.pathname + location.search);
    } catch (error) {
      setConnectionError(
        error instanceof APIError &&
          (error.status === 0 || error.status >= 500),
      );
      setError(error instanceof Error ? error.message : 'Could not sign in.');
    } finally {
      setPending(false);
    }
  }

  if (user)
    return (
      <AuthContext.Provider value={{ user, signOut }}>
        <div key={user.id} className="camp-session">
          {children}
        </div>
      </AuthContext.Provider>
    );
  return (
    <main className="startup-screen">
      <form
        className="startup-panel camp-auth"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <div className="camp-auth-heading">
          <PixelIcon name="conversation" size={40} />
          <h1>
            {loading
              ? 'Opening Camp…'
              : setup
                ? 'Welcome to Camp'
                : invite
                  ? 'Join your team'
                  : 'Welcome back'}
          </h1>
        </div>
        {!loading && (
          <>
            <p>
              {setup
                ? 'Create the first administrator account. Your team’s rooms, agents, and files will live here.'
                : invite
                  ? 'Create your account to join the conversation. Everyone on your team shares the rooms.'
                  : 'A shared place for people and agents to do useful work.'}
            </p>
            {setup && (
              <label>
                Team name
                <input
                  className="p9-field"
                  required
                  maxLength={120}
                  value={teamName}
                  onChange={(event) => setTeamName(event.target.value)}
                />
              </label>
            )}
            {setup && (
              <label>
                Setup token
                <input
                  className="p9-field"
                  required
                  autoComplete="off"
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                />
                <small>
                  Use the private setup token from the file named in the server
                  terminal.
                </small>
              </label>
            )}
            {(setup || invite) && (
              <label>
                Your name
                <input
                  className="p9-field"
                  required
                  maxLength={80}
                  autoComplete="name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
            )}
            <label>
              <span id="camp-username-label">Username</span>
              <input
                className="p9-field"
                required
                minLength={3}
                maxLength={32}
                pattern="[a-z][a-z0-9_\-]{2,31}"
                autoComplete="username"
                aria-labelledby="camp-username-label"
                aria-describedby="camp-username-hint"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
              <small id="camp-username-hint">
                3–32 lowercase letters, numbers, hyphens or underscores. Start
                with a letter.
              </small>
            </label>
            <label>
              Password
              <input
                className="p9-field"
                required
                type="password"
                minLength={setup || invite ? 12 : 1}
                autoComplete={
                  setup || invite ? 'new-password' : 'current-password'
                }
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
              {(setup || invite) && <small>At least 12 characters.</small>}
            </label>
            <Button type="submit" primary disabled={pending}>
              {pending
                ? 'Please wait…'
                : setup
                  ? 'Create Team'
                  : invite
                    ? 'Join Team'
                    : 'Sign In'}
            </Button>
            {!setup && !invite && (
              <details>
                <summary>Have an invitation?</summary>
                <label>
                  Invitation token
                  <input
                    className="p9-field"
                    autoComplete="off"
                    value={token}
                    onChange={(event) => setToken(event.target.value)}
                  />
                </label>
                <Button
                  type="button"
                  disabled={!token.trim()}
                  onClick={() => {
                    setInvite(token.trim());
                    setToken('');
                  }}
                >
                  Use Invitation
                </Button>
              </details>
            )}
            {invite && (
              <Button type="button" onClick={() => setInvite('')}>
                Back to Sign In
              </Button>
            )}
          </>
        )}
        {error && (
          <p className="app-error" role="alert">
            {error}
          </p>
        )}
        {error && connectionError && (
          <Button type="button" onClick={() => void refresh()}>
            Check Connection
          </Button>
        )}
      </form>
    </main>
  );
}
