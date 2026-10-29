import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  MENU_HEIGHT,
  STRIP_HEIGHT,
  initialIconPosition,
  sanitizeLayout,
  windowIdentity,
} from '../model';
import type { MenuRequest } from '../overlays';
import type {
  DesktopAPI,
  DesktopLayout,
  DesktopProps,
  DesktopWindow,
  DesktopViewport,
  DialogOptions,
  DialogResult,
  MenuItem,
  Point,
  WindowParams,
} from '../types';
import { viewportSize } from './useViewport';

function initialState(props: DesktopProps): DesktopLayout {
  const state = sanitizeLayout(props.initialLayout, props.apps);
  if (props.initialLayout?.version === 1) return state;
  for (const initial of props.initialWindows ?? []) {
    const app = props.apps.find((candidate) => candidate.id === initial.appId);
    if (!app) continue;
    const params = initial.params ?? {};
    const id = windowIdentity(app, params);
    if (state.windows.some((window) => window.id === id)) continue;
    const index = state.windows.length;
    state.windows.push({
      id,
      appId: app.id,
      title: initial.title ?? app.title,
      params,
      x: 64 + index * 36,
      y: 56 + index * 30,
      width: app.defaultSize?.width ?? 540,
      height: app.defaultSize?.height ?? 360,
      shaded: false,
      zoomed: false,
    });
    state.activeWindowId = id;
  }
  return state;
}

/** Owns the persisted layout and stable commands shared by every application. */
export function useDesktopController(
  props: DesktopProps,
  viewport: DesktopViewport,
) {
  const [layout, setLayout] = useState<DesktopLayout>(() =>
    initialState(props),
  );
  const propsRef = useRef(props);
  propsRef.current = props;
  const [overview, setOverview] = useState(false);
  const [menu, setMenu] = useState<MenuRequest | null>(null);
  const menuCounter = useRef(0);
  const [dialogs, setDialogs] = useState<
    Array<{
      id: number;
      options: DialogOptions;
      resolve: (result: DialogResult) => void;
    }>
  >([]);
  const dialogCounter = useRef(0);
  const closeHandlers = useRef(
    new Map<string, () => boolean | Promise<boolean>>(),
  );
  const closing = useRef(new Set<string>());
  useEffect(() => {
    propsRef.current.onLayoutChange?.(layout);
  }, [layout]);

  const activateWindow = useCallback((id: string, reveal = false) => {
    setOverview(false);
    setLayout((current) => {
      const target = current.windows.find((item) => item.id === id);
      if (!target) return current;
      if (
        current.activeWindowId === id &&
        (!target.shaded || !reveal) &&
        current.windows.at(-1)?.id === id
      )
        return current;
      return {
        ...current,
        activeWindowId: id,
        windows: [
          ...current.windows.filter((item) => item.id !== id),
          { ...target, shaded: reveal ? false : target.shaded },
        ],
      };
    });
  }, []);
  const focusWindow = useCallback(
    (id: string) => activateWindow(id, true),
    [activateWindow],
  );
  const focusDesktop = useCallback(
    () =>
      setLayout((current) =>
        current.activeWindowId === null
          ? current
          : { ...current, activeWindowId: null },
      ),
    [],
  );
  const openApp = useCallback(
    (
      appId: string,
      params: WindowParams = {},
      options?: { title?: string },
    ) => {
      const app = propsRef.current.apps.find(
        (candidate) => candidate.id === appId,
      );
      if (!app) return '';
      const id = windowIdentity(app, params);
      setOverview(false);
      setLayout((current) => {
        const existing = current.windows.find((item) => item.id === id);
        const index = current.windows.length % 7;
        const next: DesktopWindow = existing
          ? {
              ...existing,
              shaded: false,
              title: options?.title ?? existing.title,
            }
          : {
              id,
              appId,
              params,
              title: options?.title ?? app.title,
              x: 60 + index * 26,
              y: 48 + index * 28,
              width: app.defaultSize?.width ?? 540,
              height: app.defaultSize?.height ?? 360,
              shaded: false,
              zoomed: false,
            };
        return {
          ...current,
          activeWindowId: id,
          windows: [...current.windows.filter((item) => item.id !== id), next],
        };
      });
      return id;
    },
    [],
  );
  const closeWindow = useCallback(async (id: string): Promise<boolean> => {
    if (closing.current.has(id)) return false;
    closing.current.add(id);
    try {
      if ((await closeHandlers.current.get(id)?.()) === false) return false;
      setLayout((current) => {
        const remaining = current.windows.filter((item) => item.id !== id);
        return {
          ...current,
          windows: remaining,
          activeWindowId:
            current.activeWindowId === id
              ? (remaining.at(-1)?.id ?? null)
              : current.activeWindowId,
        };
      });
      return true;
    } finally {
      closing.current.delete(id);
    }
  }, []);
  const setTitle = useCallback(
    (id: string, title: string) =>
      setLayout((current) =>
        current.windows.find((win) => win.id === id)?.title === title
          ? current
          : {
              ...current,
              windows: current.windows.map((win) =>
                win.id === id ? { ...win, title } : win,
              ),
            },
      ),
    [],
  );
  const arrangeIcons = useCallback(
    () =>
      setLayout((current) => ({
        ...current,
        icons: Object.fromEntries(
          propsRef.current.icons.map((icon, index) => [
            icon.id,
            initialIconPosition(index, viewportSize()),
          ]),
        ),
      })),
    [],
  );
  const openMenu = useCallback((request: Omit<MenuRequest, 'id'>) => {
    if (request.items.length)
      setMenu({
        ...request,
        id: ++menuCounter.current,
      });
  }, []);
  const contextMenu = useCallback(
    (items: MenuItem[], point: Point) => {
      openMenu({
        items,
        point,
        opener:
          document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null,
      });
    },
    [openMenu],
  );
  const dialog = useCallback(
    (options: DialogOptions) =>
      new Promise<DialogResult>((resolve) => {
        setMenu(null);
        setDialogs((queue) => [
          ...queue,
          { id: ++dialogCounter.current, options, resolve },
        ]);
      }),
    [],
  );
  const beforeClose = useCallback(
    (id: string, handler: () => boolean | Promise<boolean>) => {
      closeHandlers.current.set(id, handler);
      return () => {
        if (closeHandlers.current.get(id) === handler)
          closeHandlers.current.delete(id);
      };
    },
    [],
  );
  const api = useMemo<DesktopAPI>(
    () => ({
      windows: layout.windows,
      activeWindowId: layout.activeWindowId,
      openApp,
      closeWindow,
      focusWindow,
      setTitle,
      arrangeIcons,
      contextMenu,
      dialog,
      beforeClose,
      showAllWindows: () => setOverview((current) => !current),
    }),
    [
      layout.windows,
      layout.activeWindowId,
      openApp,
      closeWindow,
      focusWindow,
      setTitle,
      arrangeIcons,
      contextMenu,
      dialog,
      beforeClose,
    ],
  );
  useEffect(() => {
    propsRef.current.onReady?.(api);
  }, [api]);
  const toggleShade = (id: string) =>
    setLayout((current) => ({
      ...current,
      windows: current.windows.map((win) =>
        win.id === id ? { ...win, shaded: !win.shaded } : win,
      ),
    }));
  const toggleZoom = (id: string) =>
    setLayout((current) => ({
      ...current,
      windows: current.windows.map((win) =>
        win.id !== id
          ? win
          : win.zoomed
            ? {
                ...win,
                ...(win.restoreBounds ?? win),
                zoomed: false,
                restoreBounds: undefined,
              }
            : {
                ...win,
                restoreBounds: {
                  x: win.x,
                  y: win.y,
                  width: win.width,
                  height: win.height,
                },
                x: 6,
                y: (viewport.menuHeight ?? MENU_HEIGHT) + 8,
                width: viewport.width - 12,
                height:
                  viewport.height -
                  (viewport.menuHeight ?? MENU_HEIGHT) -
                  (viewport.stripHeight ?? STRIP_HEIGHT) -
                  22,
                shaded: false,
                zoomed: true,
              },
      ),
    }));
  const dialogRequest = dialogs[0];
  const resolveDialog = (result: DialogResult) => {
    if (!dialogRequest) return;
    dialogRequest.resolve(result);
    setDialogs((current) => current.slice(1));
  };
  return {
    layout,
    setLayout,
    overview,
    setOverview,
    menu,
    openMenu,
    dismissMenu: () => setMenu(null),
    dialogRequest,
    resolveDialog,
    api,
    activateWindow,
    focusDesktop,
    toggleShade,
    toggleZoom,
  };
}
