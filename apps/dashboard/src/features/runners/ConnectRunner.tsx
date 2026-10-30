import { useEffect, useState } from 'react';
import { Button, Select } from '@cofy-x/camp-platinum';
import { useTeam } from '../../team/TeamContext';
import { runnersApi, type Enrollment, type EnrollmentRecord } from './api';
import { runnerSetupCommand } from './presentation';

function size(bytes?: number) {
  return bytes ? `${Math.ceil(bytes / 1_000_000)} MB` : '';
}

export function ConnectRunner({
  onConnected,
}: {
  onConnected: (id: string) => void;
}) {
  const { revision, refresh } = useTeam();
  const [name, setName] = useState('');
  const [platform, setPlatform] = useState('linux-amd64');
  const [observedInstallation, setInstallation] = useState<Awaited<
    ReturnType<typeof runnersApi.installation>
  > | null>(null);
  const [installationError, setInstallationError] = useState('');
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [enrollments, setEnrollments] = useState<EnrollmentRecord[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [codesError, setCodesError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    void runnersApi
      .installation(controller.signal, platform)
      .then((value) => {
        if (!controller.signal.aborted) {
          if (value.target !== platform)
            throw new Error(
              'The download does not match the selected platform. Retry the download.',
            );
          setInstallation(value);
          setInstallationError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setInstallationError(
            error instanceof Error
              ? error.message
              : 'Could not load the Runner download.',
          );
      });
    return () => controller.abort();
  }, [revision, platform]);
  useEffect(() => {
    const controller = new AbortController();
    void runnersApi
      .enrollments(controller.signal)
      .then(({ enrollments }) => {
        if (!controller.signal.aborted) {
          setEnrollments(enrollments ?? []);
          setCodesError('');
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setCodesError(
            error instanceof Error
              ? error.message
              : 'Could not load connection codes.',
          );
      });
    return () => controller.abort();
  }, [revision]);
  const mac = platform === 'darwin-arm64';
  const installation =
    observedInstallation?.target === platform ? observedInstallation : null;
  const latest =
    enrollments.find((item) => item.id === enrollment?.enrollment.id) ??
    enrollment?.enrollment;
  const consumed = latest?.runnerId;
  const expired = latest && new Date(latest.expiresAt).getTime() <= Date.now();
  const visibleCode = enrollment && !latest?.revoked && !consumed && !expired;
  const unused = enrollments.filter(
    (item) =>
      !item.revoked &&
      !item.runnerId &&
      new Date(item.expiresAt).getTime() > Date.now() &&
      item.id !== enrollment?.enrollment.id,
  );

  async function revoke(id: string) {
    setPending(true);
    setError('');
    setNotice('');
    try {
      await runnersApi.revokeEnrollment(id);
      setEnrollment((current) =>
        current?.enrollment.id === id ? null : current,
      );
      setNotice('Connection code revoked.');
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not revoke this connection code.',
      );
    } finally {
      setPending(false);
    }
  }

  async function enroll() {
    if (!name.trim() || pending) return;
    setPending(true);
    setError('');
    setNotice('');
    try {
      setEnrollment(await runnersApi.enroll(name.trim()));
      setName('');
      refresh();
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Could not create a connection code.',
      );
      refresh();
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="camp-runner-connect" aria-label="Connect Runner">
      <h2>Connect a machine</h2>
      <section
        className="camp-runner-connect-step"
        aria-label="Install Camp Runner"
      >
        <h3>1. Get Camp Runner</h3>
        <label>
          Runner platform
          <Select
            aria-label="Runner platform"
            value={platform}
            onChange={(event) => {
              setPlatform(event.target.value);
              setInstallation(null);
              setInstallationError('');
            }}
          >
            <option value="linux-amd64">Ubuntu 24.04 amd64</option>
            <option value="darwin-arm64">
              Mac · Apple Silicon · macOS 15+
            </option>
          </Select>
        </label>
        <p>
          {mac
            ? 'Run setup and the Runner as your regular Mac account, without sudo.'
            : 'Ubuntu 24.04 amd64. Run setup and the Runner as root, using the same private data directory.'}
        </p>
        {installationError ? (
          <p className="app-error" role="alert">
            {installationError}{' '}
            <Button onClick={refresh}>Retry Download</Button>
          </p>
        ) : !installation ? (
          <p role="status">Checking download…</p>
        ) : (
          <>
            {installation.available ? (
              <>
                <div className="app-actions">
                  <a
                    className="camp-link"
                    href={installation.downloadUrl}
                    download={installation.filename}
                  >
                    Download for {mac ? 'Mac' : 'Linux'}
                    {installation.bytes ? ` (${size(installation.bytes)})` : ''}
                  </a>
                  {installation.checksumUrl && (
                    <a
                      className="camp-link"
                      href={installation.checksumUrl}
                      download
                    >
                      Checksum
                    </a>
                  )}
                </div>
                <p className="camp-runner-hint">
                  {installation.version && `${installation.version}. `}
                  Extract on the Runner machine.
                  {installation.runtime?.bytes
                    ? ` Setup downloads ${size(installation.runtime.bytes)} once.`
                    : ''}
                </p>
              </>
            ) : (
              <p>
                The installer is unavailable for this build. Use a published
                Camp release or ask an administrator to stage matching packages.
                {installation.offline?.available &&
                  ' The full offline package is available below.'}
              </p>
            )}
            {(installation.available || installation.offline?.available) && (
              <details className="camp-runner-install-options">
                <summary>Requirements and offline install</summary>
                <p>
                  {mac
                    ? 'Requires Git. Executes Mac tools in a private local workspace with Seatbelt. Docker, Linux and FUSE are not required.'
                    : 'Requires Docker Engine, FUSE, Bubblewrap, Git and Bash.'}
                </p>
                {installation.source === 'server' && installation.version && (
                  <p>
                    This Server mirrors the fixed release. For setup without
                    GitHub access, add <code>--runtime-source server</code>.
                  </p>
                )}
                <p>
                  Download the checksum beside the archive, then verify it
                  before extracting:
                </p>
                <pre>
                  <code>{`${mac ? 'shasum -a 256' : 'sha256sum'} -c ${installation.available ? installation.filename : installation.offline!.filename}.sha256\ntar -xzf ${installation.available ? installation.filename : installation.offline!.filename}`}</code>
                </pre>
                {installation.offline?.available && (
                  <p>
                    <a
                      className="camp-link"
                      href={installation.offline.downloadUrl}
                      download={installation.offline.filename}
                    >
                      Full offline package
                      {installation.offline.bytes
                        ? ` (${size(installation.offline.bytes)})`
                        : ''}
                    </a>
                    {' · '}
                    <a
                      className="camp-link"
                      href={installation.offline.checksumUrl}
                      download
                    >
                      Offline checksum
                    </a>
                  </p>
                )}
                {installation.runtime?.unpackedBytes ? (
                  <p>
                    Execution tools use approximately{' '}
                    {size(installation.runtime.unpackedBytes)} on disk.
                  </p>
                ) : null}
              </details>
            )}
          </>
        )}
      </section>
      <section
        className="camp-runner-connect-step"
        aria-label="Connect this machine"
      >
        <h3>2. Connect this machine</h3>
        {consumed ? (
          <>
            <p role="status">
              {latest.name} registered. Finish setup and start the Runner on its
              machine.
            </p>
            <div className="app-actions">
              <Button
                primary
                onClick={() => {
                  setEnrollment(null);
                  setNotice('');
                  setError('');
                  onConnected(consumed);
                }}
              >
                Open Runner
              </Button>
              <Button onClick={() => setEnrollment(null)}>
                Connect Another…
              </Button>
            </div>
          </>
        ) : visibleCode ? (
          <section
            className="camp-runner-code"
            aria-label="One-time connection code"
          >
            <p>In the extracted folder, run:</p>
            <pre>
              <code>
                {runnerSetupCommand(
                  globalThis.location?.origin,
                  enrollment.enrollment.name,
                )}
              </code>
            </pre>
            <p className="camp-runner-hint">
              With the same Camp version installed through Homebrew or Go, use{' '}
              <code>camp</code> instead of <code>./camp</code>.
            </p>
            <p>
              Paste this code when setup asks. It is shown once; save it before
              hiding.
            </p>
            <label>
              Connection code
              <textarea
                className="p9-field camp-runner-token"
                aria-label="Connection code"
                value={enrollment.token}
                readOnly
                rows={2}
                spellCheck={false}
              />
            </label>
            <div className="app-actions">
              <Button
                onClick={() =>
                  void navigator.clipboard
                    .writeText(enrollment.token)
                    .then(() => setNotice('Connection code copied.'))
                    .catch(() =>
                      setError(
                        'Could not copy. Select the code and copy it manually.',
                      ),
                    )
                }
              >
                Copy Code
              </Button>
              <Button disabled={pending} onClick={() => setEnrollment(null)}>
                Hide Code
              </Button>
              <Button
                disabled={pending}
                onClick={() => void revoke(enrollment.enrollment.id)}
              >
                Revoke Code
              </Button>
            </div>
            <p className="camp-runner-hint">
              Expires{' '}
              {new Date(enrollment.enrollment.expiresAt).toLocaleString()}.
            </p>
            <p>
              Start the Runner with setup’s run command. Background service
              setup is optional.
            </p>
            <p role="status">
              Waiting for registration…{' '}
              <Button onClick={refresh}>Check Connection</Button>
            </p>
          </section>
        ) : (
          <>
            {(latest?.revoked || expired) && (
              <p className="app-error">
                This connection code {expired ? 'expired' : 'was revoked'}.
                Create a new code to connect.
              </p>
            )}
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void enroll();
              }}
            >
              <label>
                Runner name
                <input
                  className="p9-field"
                  aria-label="Runner name"
                  value={name}
                  maxLength={100}
                  disabled={pending}
                  onChange={(event) => setName(event.target.value)}
                  placeholder={
                    mac
                      ? 'For example, Studio Mac'
                      : 'For example, Studio Linux'
                  }
                />
              </label>
              <Button primary type="submit" disabled={pending || !name.trim()}>
                {pending ? 'Creating…' : 'Create Connection Code'}
              </Button>
            </form>
          </>
        )}
      </section>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {codesError && (
        <p className="app-error" role="alert">
          {codesError} <Button onClick={refresh}>Retry Codes</Button>
        </p>
      )}
      {unused.length > 0 && (
        <details className="camp-runner-pending-codes">
          <summary>Pending connection codes ({unused.length})</summary>
          <p className="camp-runner-hint">
            Hidden codes cannot be shown again. Revoke any you no longer need.
          </p>
          <ul>
            {unused.map((item) => (
              <li key={item.id}>
                <div>
                  <strong>{item.name}</strong>
                  <small>
                    Expires {new Date(item.expiresAt).toLocaleString()}
                  </small>
                </div>
                <Button disabled={pending} onClick={() => void revoke(item.id)}>
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}
