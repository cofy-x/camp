import { useRef } from 'react';
import type { CSSProperties, MouseEvent } from 'react';
import { DialogOverlay, MenuOverlay } from './overlays';
import type { DesktopProps, MenuGroup, MenuItem } from './types';
import { ControlStrip } from './desktop/ControlStrip';
import { DesktopCanvas } from './desktop/DesktopCanvas';
import { MenuBar } from './desktop/MenuBar';
import { WindowFrame } from './desktop/WindowFrame';
import { createDesktopContextMenu, createDesktopMenus } from './desktop/menus';
import { useDesktopController } from './desktop/useDesktopController';
import { useDesktopGestures } from './desktop/useDesktopGestures';
import { useViewport } from './desktop/useViewport';

export function Desktop(props: DesktopProps) {
  const menuBar = useRef<HTMLDivElement>(null);
  const viewport = useViewport();
  const mobile = viewport.width < 600;
  const controller = useDesktopController(props, viewport);
  const { layout, overview, setOverview, api, menu, dialogRequest } =
    controller;
  const desktopItems = (ids: string[]) =>
    createDesktopContextMenu(props, api, ids);
  const gestures = useDesktopGestures({
    props,
    layout,
    setLayout: controller.setLayout,
    viewport,
    mobile,
    overview,
    setOverview,
    overlayActive: !!menu || !!dialogRequest,
    api,
    activateWindow: controller.activateWindow,
    focusDesktop: controller.focusDesktop,
    desktopItems,
  });
  const { groups, windowItems } = createDesktopMenus(
    props,
    api,
    gestures.selection,
  );
  const activeWindow = layout.windows.find(
    (win) => win.id === layout.activeWindowId,
  );
  const activeApp = props.apps.find((app) => app.id === activeWindow?.appId);
  const applicationMenu: MenuGroup = {
    id: 'platinum-applications',
    label: 'Applications',
    items: windowItems,
  };
  const openMenuGroup = (
    group: MenuGroup,
    opener: HTMLButtonElement,
    initialSelection: 'first' | 'last' = 'first',
  ) => {
    opener.focus({ preventScroll: true });
    const rect = opener.getBoundingClientRect();
    controller.openMenu({
      items: group.items,
      point: { x: rect.left, y: rect.bottom },
      opener,
      menuBarId: group.id,
      initialSelection,
    });
  };
  const switchMenu = (direction: -1 | 1) => {
    const allGroups = [...groups, applicationMenu];
    const index = allGroups.findIndex((group) => group.id === menu?.menuBarId);
    if (index < 0) return;
    const next =
      allGroups[(index + direction + allGroups.length) % allGroups.length];
    const opener = Array.from(
      menuBar.current?.querySelectorAll<HTMLButtonElement>('[data-menu-id]') ??
        [],
    ).find((button) => button.dataset.menuId === next.id);
    if (opener) openMenuGroup(next, opener);
  };
  const openMenu = (
    event: MouseEvent<HTMLButtonElement>,
    items: MenuItem[],
  ) => {
    event.currentTarget.focus({ preventScroll: true });
    const rect = event.currentTarget.getBoundingClientRect();
    api.contextMenu(items, { x: rect.left, y: rect.bottom });
  };

  return (
    <div
      className={`p9-desktop p9-wallpaper-${props.wallpaper ?? 'lavender'} ${mobile ? 'is-mobile' : ''} ${overview ? 'is-overview' : ''}`}
      style={
        { '--p9-viewport-height': `${viewport.height}px` } as CSSProperties
      }
    >
      <MenuBar
        elementRef={menuBar}
        groups={groups}
        applicationMenu={applicationMenu}
        activeApp={activeApp}
        activeMenuId={menu?.menuBarId}
        menuElementId={menu ? `p9-menu-${menu.id}` : undefined}
        dismissMenu={controller.dismissMenu}
        overview={overview}
        onShowAllWindows={api.showAllWindows}
        openMenu={openMenuGroup}
      />
      <DesktopCanvas
        icons={props.icons}
        onTrashIcons={props.onTrashIcons}
        gestures={gestures}
      />
      {overview && (
        <div
          className="p9-overview-backdrop"
          onClick={() => setOverview(false)}
        >
          <span>Choose a window · Escape to return</span>
        </div>
      )}
      {layout.windows.map((win, index) => {
        const app = props.apps.find((item) => item.id === win.appId);
        return app ? (
          <WindowFrame
            key={win.id}
            win={win}
            app={app}
            index={index}
            api={api}
            viewport={viewport}
            mobile={mobile}
            overview={overview}
            preview={gestures.windowPreview[win.id]}
            activateWindow={controller.activateWindow}
            beginWindowGesture={gestures.beginWindowGesture}
            toggleShade={controller.toggleShade}
            toggleZoom={controller.toggleZoom}
          />
        ) : null;
      })}
      <ControlStrip
        controlStrip={props.controlStrip}
        onControlStripChange={props.onControlStripChange}
        wallpaper={props.wallpaper}
        connectionStatus={props.connectionStatus}
        viewportWidth={viewport.width}
        appearance={props.onAppearance}
        windowItems={windowItems}
        openMenu={openMenu}
      />
      {menu && (
        <MenuOverlay
          key={menu.id}
          request={menu}
          close={controller.dismissMenu}
          switchMenu={menu.menuBarId ? switchMenu : undefined}
        />
      )}
      {dialogRequest && (
        <DialogOverlay
          key={dialogRequest.id}
          options={dialogRequest.options}
          resolve={controller.resolveDialog}
        />
      )}
    </div>
  );
}
