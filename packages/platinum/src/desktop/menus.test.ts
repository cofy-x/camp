import { describe, expect, it, vi } from 'vitest';
import type { AppDefinition, DesktopAPI, DesktopProps } from '../types';
import { createDesktopContextMenu, createDesktopMenus } from './menus';

function desktopAPI(): DesktopAPI {
  return {
    windows: [],
    activeWindowId: null,
    getActivationRevision: vi.fn(() => 0),
    openApp: vi.fn(() => 'opened'),
    closeWindow: vi.fn(async () => true),
    focusWindow: vi.fn(),
    setTitle: vi.fn(),
    showAllWindows: vi.fn(),
    arrangeIcons: vi.fn(),
    contextMenu: vi.fn(),
    dialog: vi.fn(async () => null),
    beforeClose: vi.fn(() => () => {}),
  };
}

function app(id: string, launchable?: boolean): AppDefinition {
  return { id, title: id, icon: 'document', component: () => null, launchable };
}

describe('host-independent desktop menus', () => {
  it('launches arbitrary app IDs in registration order without reserving sample names', () => {
    const api = desktopAPI();
    const props: DesktopProps = {
      apps: [
        app('inventory'),
        app('invoice-editor', false),
        app('about'),
        app('reporting'),
      ],
      icons: [],
    };
    const { groups } = createDesktopMenus(props, api, []);
    const system = groups.find((group) => group.id === 'desktop')!;
    expect(system.items.map((item) => item.label ?? 'separator')).toEqual([
      'inventory',
      'about',
      'reporting',
      'separator',
      'Windows',
    ]);
    system.items[0].action?.();
    system.items[1].action?.();
    system.items[2].action?.();
    expect(api.openApp).toHaveBeenNthCalledWith(1, 'inventory');
    expect(api.openApp).toHaveBeenNthCalledWith(2, 'about');
    expect(api.openApp).toHaveBeenNthCalledWith(3, 'reporting');
    expect(groups.map((group) => group.id)).toEqual([
      'desktop',
      'file',
      'view',
      'window',
    ]);
    expect(
      groups
        .find((group) => group.id === 'view')!
        .items.map((item) => item.label),
    ).toEqual(['Clean Up Desktop', 'Show All Windows']);
  });

  it('routes optional shell actions through host callbacks and keeps their menu positions', () => {
    const api = desktopAPI();
    const onAbout = vi.fn();
    const onHelp = vi.fn();
    const onAppearance = vi.fn();
    const props: DesktopProps = {
      apps: [app('inventory')],
      icons: [],
      onAbout,
      onHelp,
      onAppearance,
      helpLabel: 'User Guide',
    };
    const { groups } = createDesktopMenus(props, api, []);
    const system = groups.find((group) => group.id === 'desktop')!;
    expect(system.items.map((item) => item.label ?? 'separator')).toEqual([
      'About This Desktop…',
      'separator',
      'inventory',
      'separator',
      'Windows',
    ]);
    system.items[0].action?.();
    const help = groups.find((group) => group.id === 'help')!;
    expect(help.items.map((item) => item.label)).toEqual([
      'User Guide',
      'About This Desktop…',
    ]);
    help.items[0].action?.();
    help.items[1].action?.();
    groups
      .find((group) => group.id === 'view')!
      .items.at(-1)!
      .action?.();
    expect(onAbout).toHaveBeenCalledTimes(2);
    expect(onHelp).toHaveBeenCalledOnce();
    expect(onAppearance).toHaveBeenCalledOnce();
    expect(api.openApp).not.toHaveBeenCalled();
  });

  it('names the selected application instead of implying every Open command opens files', () => {
    const api = desktopAPI();
    const props: DesktopProps = {
      apps: [app('archive'), app('preferences')],
      icons: [
        { id: 'files', label: 'Library', icon: 'folder', appId: 'archive' },
        {
          id: 'settings',
          label: 'Settings',
          icon: 'settings',
          appId: 'preferences',
        },
      ],
    };
    const file = (selection: string[]) =>
      createDesktopMenus(props, api, selection).groups.find(
        (group) => group.id === 'file',
      )!;
    expect(file([]).items[0]).toMatchObject({
      label: 'Open Selected',
      disabled: true,
    });
    expect(file(['missing']).items[0].disabled).toBe(true);
    expect(file(['files']).items[0].label).toBe('Open Library');
    expect(file(['settings']).items[0].label).toBe('Open Settings');
    expect(file(['files', 'settings']).items[0].label).toBe('Open Selected');
    file(['settings']).items[0].action?.();
    expect(api.openApp).toHaveBeenCalledExactlyOnceWith(
      'preferences',
      undefined,
    );
  });

  it('opens selected icons through their host action or app identity and preserves custom context menus', () => {
    const api = desktopAPI();
    const onOpen = vi.fn();
    const props: DesktopProps = {
      apps: [app('inventory')],
      icons: [
        {
          id: 'sku',
          label: 'SKU',
          icon: 'document',
          appId: 'inventory',
          params: { sku: '42' },
        },
        { id: 'custom', label: 'Custom', icon: 'folder', onOpen },
      ],
    };
    const menu = createDesktopContextMenu(props, api, ['sku', 'custom']);
    menu[0].action?.();
    expect(api.openApp).toHaveBeenCalledExactlyOnceWith('inventory', {
      sku: '42',
    });
    expect(onOpen).toHaveBeenCalledOnce();
    expect(menu.map((item) => item.label ?? 'separator')).toEqual([
      'Open',
      'separator',
      'Clean Up Desktop',
    ]);
    expect(
      createDesktopContextMenu({ ...props, onDesktopMenu: () => [] }, api, [
        'sku',
      ]),
    ).toEqual([]);
  });
});
