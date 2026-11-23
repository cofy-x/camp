import { useCallback, useMemo, useRef, type ComponentProps } from 'react';
import { Button, Desktop } from '@cofy-x/camp-platinum';
import type { DesktopAPI } from '@cofy-x/camp-platinum';
import { PreferencesContext } from '../features/appearance/PreferencesContext';
import { CampMark } from '../shared/desktop/CampMark';
import { runCommand } from '../shared/desktop/dialogs';
import { createAppRegistry } from './registry';
import { createDesktopIcons, createDesktopMenu } from './desktop-items';
import { useDesktopSession } from './useDesktopSession';
import { AuthGate } from '../team/AuthGate';
import { useTeam, TeamProvider } from '../team/TeamContext';
import { DesktopInbox } from '../team/DesktopInbox';
import { useAccount } from '../team/AuthContext';
import { ReadProvider } from '../shared/reads/useRead';

const controlStripApps = ['workspace', 'work', 'library'] as const;

export function App() {
  return (
    <AuthGate>
      <CampDesktop />
    </AuthGate>
  );
}

function CampDesktop() {
  const { user } = useAccount();
  const session = useDesktopSession();
  const {
    bootstrap,
    bootError,
    boot,
    appearance,
    connection,
    persistenceError,
    onLayoutChange,
    flushLayout,
  } = session;
  const { preferences, savePreferences } = appearance;
  const desktopRef = useRef<DesktopAPI | null>(null);
  const getDesktop = useCallback(() => desktopRef.current, []);
  const apps = useMemo(createAppRegistry, []);
  const icons = useMemo(createDesktopIcons, []);
  const openApp = useCallback((appId: string) => {
    desktopRef.current?.openApp(appId);
  }, []);
  const getActivationRevision = useCallback(
    () => desktopRef.current?.getActivationRevision() ?? 0,
    [],
  );

  if (!bootstrap)
    return (
      <div className="startup-screen">
        <div className="startup-panel">
          <CampMark size={64} />
          <strong>{document.title}</strong>
          {bootError ? (
            <>
              <p role="alert">{bootError}</p>
              <p>Make sure the desktop server is running, then try again.</p>
              <Button primary onClick={() => void boot()}>
                Try Again
              </Button>
            </>
          ) : (
            <p>Starting your desktop…</p>
          )}
        </div>
      </div>
    );

  return (
    <ReadProvider scope={user.id}>
      <TeamProvider
        initialCursor={bootstrap.workspaceEventCursor}
        openApp={openApp}
        getActivationRevision={getActivationRevision}
      >
        <PreferencesContext.Provider value={appearance}>
          <TeamDesktop
            apps={apps}
            icons={icons}
            systemMenuIcon={<CampMark size={16} />}
            initialLayout={bootstrap.layout || undefined}
            initialWindows={[{ appId: 'workspace' }]}
            wallpaper={preferences.wallpaper}
            controlStrip={preferences.controlStrip}
            controlStripApps={controlStripApps}
            iconSnap={preferences.iconSnap}
            connectionStatus={connection}
            onReady={(desktop) => {
              desktopRef.current = desktop;
            }}
            onLayoutChange={onLayoutChange}
            onDesktopMenu={(_, desktop) => createDesktopMenu(desktop)}
            onAbout={() => getDesktop()?.openApp('about')}
            onControlStripChange={(controlStrip) => {
              const desktop = getDesktop();
              if (desktop)
                void runCommand(desktop, () =>
                  savePreferences({ controlStrip }),
                );
            }}
          />
          {persistenceError && (
            <div className="persistence-warning" role="alert">
              <span>{persistenceError}</span>
              <Button onClick={() => void flushLayout()}>Retry</Button>
            </div>
          )}
        </PreferencesContext.Provider>
      </TeamProvider>
    </ReadProvider>
  );
}

function TeamDesktop(props: ComponentProps<typeof Desktop>) {
  const team = useTeam();
  const { user } = useAccount();
  return (
    <>
      <Desktop
        {...props}
        onAppearance={() =>
          void team.navigate({ app: 'settings', section: 'appearance' })
        }
        helpLabel="Getting Ready"
        onHelp={
          user.role === 'admin'
            ? () =>
                void team.navigate({ app: 'settings', section: 'readiness' })
            : undefined
        }
      />
      <DesktopInbox />
    </>
  );
}
