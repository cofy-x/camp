import { mergeMenuGroups } from '../model';
import type { DesktopAPI, DesktopProps, MenuGroup, MenuItem } from '../types';

function openSelectedIcons(
  props: DesktopProps,
  api: DesktopAPI,
  ids: string[],
) {
  for (const icon of props.icons.filter((item) => ids.includes(item.id))) {
    if (icon.onOpen) icon.onOpen();
    else if (icon.appId) api.openApp(icon.appId, icon.params);
  }
}

export function createDesktopContextMenu(
  props: DesktopProps,
  api: DesktopAPI,
  ids: string[],
): MenuItem[] {
  const custom = props.onDesktopMenu?.(ids, api);
  if (custom) return custom;
  return [
    {
      label: 'Open',
      disabled: !ids.length,
      action: () => openSelectedIcons(props, api, ids),
    },
    { separator: true },
    { label: 'Clean Up Desktop', action: api.arrangeIcons },
    ...(props.onAppearance
      ? [{ label: 'Change Desktop Appearance…', action: props.onAppearance }]
      : []),
  ];
}

/** Application identities stay in the host; the shell only builds generic commands. */
export function createDesktopMenus(
  props: DesktopProps,
  api: DesktopAPI,
  selection: string[],
): { groups: MenuGroup[]; windowItems: MenuItem[] } {
  const activeWindow = api.windows.find((win) => win.id === api.activeWindowId);
  const activeApp = props.apps.find((app) => app.id === activeWindow?.appId);
  const windowItems: MenuItem[] = [
    {
      label: 'Show All Windows',
      disabled: !api.windows.length,
      action: api.showAllWindows,
    },
    { separator: true },
    ...api.windows.map((win) => ({
      id: win.id,
      label: win.title,
      checked: api.activeWindowId === win.id,
      action: () => api.focusWindow(win.id),
    })),
  ];
  const launcherItems: MenuItem[] = props.apps
    .filter((app) => app.launchable !== false)
    .map((app) => ({ label: app.title, action: () => api.openApp(app.id) }));
  const aboutItems: MenuItem[] = props.onAbout
    ? [{ label: 'About This Desktop…', action: props.onAbout }]
    : [];
  const helpItems: MenuItem[] = [
    ...(props.onHelp
      ? [{ label: props.helpLabel ?? 'Help', action: props.onHelp }]
      : []),
    ...aboutItems,
  ];
  const selectedIcons = props.icons.filter((icon) =>
    selection.includes(icon.id),
  );
  const openLabel =
    selectedIcons.length === 1
      ? `Open ${selectedIcons[0].label}`
      : 'Open Selected';
  const defaults: MenuGroup[] = [
    {
      id: 'desktop',
      label: '◆',
      items: [
        ...aboutItems,
        ...(aboutItems.length ? [{ separator: true }] : []),
        ...launcherItems,
        ...(launcherItems.length ? [{ separator: true }] : []),
        { label: 'Windows', children: windowItems },
      ],
    },
    {
      id: 'file',
      label: 'File',
      items: [
        {
          label: openLabel,
          disabled: !selectedIcons.length,
          shortcut: '⌘O',
          action: () => openSelectedIcons(props, api, selection),
        },
        {
          label: 'Close Window',
          disabled: !activeWindow,
          shortcut: '⌘W',
          action: () => {
            if (activeWindow) void api.closeWindow(activeWindow.id);
          },
        },
      ],
    },
    {
      id: 'view',
      label: 'View',
      items: [
        { label: 'Clean Up Desktop', action: api.arrangeIcons },
        {
          label: 'Show All Windows',
          action: api.showAllWindows,
          disabled: !api.windows.length,
        },
        ...(props.onAppearance
          ? [
              { separator: true },
              { label: 'Appearance…', action: props.onAppearance },
            ]
          : []),
      ],
    },
    { id: 'window', label: 'Window', items: windowItems },
    ...(helpItems.length
      ? [{ id: 'help', label: 'Help', items: helpItems }]
      : []),
  ];
  return {
    groups: mergeMenuGroups(
      defaults,
      activeWindow && activeApp?.menu ? activeApp.menu(activeWindow, api) : [],
    ),
    windowItems,
  };
}
