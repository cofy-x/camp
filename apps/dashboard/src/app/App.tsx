import { useCallback, useMemo, useRef, type ComponentProps } from 'react';
import { Button, Desktop, PixelIcon } from '@cofy-x/camp-platinum';
import type { DesktopAPI } from '@cofy-x/camp-platinum';
import { PreferencesContext } from '../features/appearance/PreferencesContext';
import { runCommand } from '../shared/desktop/dialogs';
import { createAppRegistry } from './registry';
import { createDesktopIcons, createDesktopMenu } from './desktop-items';
import { useDesktopSession } from './useDesktopSession';
import { AuthGate } from '../team/AuthGate';
import { useTeam, TeamProvider } from '../team/TeamContext';
import { DesktopInbox } from '../team/DesktopInbox';

export function App() {
  return (
    <AuthGate>
      <CampDesktop />
    </AuthGate>
  );
}

function CampDesktop() {
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
          <PixelIcon name="computer" size={64} />
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
    <TeamProvider
      openApp={openApp}
      getActivationRevision={getActivationRevision}
    >
      <PreferencesContext.Provider value={appearance}>
        <TeamDesktop
          apps={apps}
          icons={icons}
          initialLayout={bootstrap.layout || undefined}
          initialWindows={[{ appId: 'workspace' }]}
          wallpaper={preferences.wallpaper}
          controlStrip={preferences.controlStrip}
          iconSnap={preferences.iconSnap}
          connectionStatus={connection}
          onReady={(desktop) => {
            desktopRef.current = desktop;
          }}
          onLayoutChange={onLayoutChange}
          onDesktopMenu={(_, desktop) => createDesktopMenu(desktop)}
          onAppearance={() => getDesktop()?.openApp('appearance')}
          onAbout={() => getDesktop()?.openApp('about')}
          onHelp={() => getDesktop()?.openApp('settings')}
          helpLabel="Getting Ready"
          onControlStripChange={(controlStrip) => {
            const desktop = getDesktop();
            if (desktop)
              void runCommand(desktop, () => savePreferences({ controlStrip }));
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
  );
}

function TeamDesktop(props: ComponentProps<typeof Desktop>) {
  const team = useTeam();
  return (
    <>
      <Desktop
        {...props}
        onAppearance={() =>
          void team.navigate({ app: 'settings', section: 'appearance' })
        }
        onHelp={() =>
          void team.navigate({ app: 'settings', section: 'readiness' })
        }
      />
      <DesktopInbox />
    </>
  );
}
