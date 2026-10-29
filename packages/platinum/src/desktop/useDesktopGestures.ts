import { useEffect, useRef, useState } from 'react';
import type {
  Dispatch,
  SetStateAction,
  PointerEvent as ReactPointerEvent,
} from 'react';
import {
  ICON_SIZE,
  MENU_HEIGHT,
  STRIP_HEIGHT,
  initialIconPosition,
  intersects,
  marqueeBounds,
  moveIconGroup,
  resizeBounds,
  selectionAfterClick,
  visibleBounds,
} from '../model';
import type {
  Bounds,
  DesktopAPI,
  DesktopIcon,
  DesktopLayout,
  DesktopProps,
  DesktopWindow,
  DesktopViewport,
  MenuItem,
  Point,
} from '../types';
import { usePointerGesture } from './usePointerGesture';

interface GestureOptions {
  props: Pick<DesktopProps, 'apps' | 'icons' | 'iconSnap' | 'onTrashIcons'>;
  layout: DesktopLayout;
  setLayout: Dispatch<SetStateAction<DesktopLayout>>;
  viewport: DesktopViewport;
  mobile: boolean;
  overview: boolean;
  setOverview: Dispatch<SetStateAction<boolean>>;
  overlayActive: boolean;
  api: DesktopAPI;
  activateWindow: (id: string) => void;
  focusDesktop: () => void;
  desktopItems: (ids: string[]) => MenuItem[];
}

const editable = (target: EventTarget | null) =>
  target instanceof HTMLElement &&
  !!target.closest('input, textarea, select, [contenteditable="true"]');

/** Preview pointer movement locally, then commit once a gesture completes. */
export function useDesktopGestures({
  props,
  layout,
  setLayout,
  viewport,
  mobile,
  overview,
  setOverview,
  overlayActive,
  api,
  activateWindow,
  focusDesktop,
  desktopItems,
}: GestureOptions) {
  const { contextMenu } = api;
  const beginGesture = usePointerGesture();
  const [selection, setSelection] = useState<string[]>([]);
  const selectionRef = useRef(selection);
  selectionRef.current = selection;
  const [windowPreview, setWindowPreview] = useState<Record<string, Bounds>>(
    {},
  );
  const [iconPreview, setIconPreview] = useState<Record<string, Point>>({});
  const [marquee, setMarquee] = useState<Bounds | null>(null);
  const [trashHover, setTrashHover] = useState(false);
  const desktopRef = useRef<HTMLDivElement>(null);
  const lastTouch = useRef<{ id: string; time: number } | null>(null);
  const lastTouchActivation = useRef<{ id: string; time: number } | null>(null);
  const iconPosition = (icon: DesktopIcon, index: number): Point => {
    const point =
      iconPreview[icon.id] ??
      layout.icons[icon.id] ??
      initialIconPosition(index, viewport);
    return {
      x: Math.max(0, Math.min(point.x, viewport.width - ICON_SIZE.width)),
      y: Math.max(
        (viewport.menuHeight ?? MENU_HEIGHT) + 6,
        Math.min(
          point.y,
          viewport.height -
            ICON_SIZE.height -
            (viewport.stripHeight ?? STRIP_HEIGHT) -
            8,
        ),
      ),
    };
  };
  const openIcon = (icon: DesktopIcon) => {
    if (icon.onOpen) icon.onOpen();
    else if (icon.appId) api.openApp(icon.appId, icon.params);
  };
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (overlayActive) return;
      if (event.key === 'Escape') {
        setOverview(false);
        setSelection([]);
      }
      if (
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === 'w' &&
        api.activeWindowId
      ) {
        event.preventDefault();
        void api.closeWindow(api.activeWindowId);
      }
      if (editable(event.target)) return;
      if (!desktopRef.current?.contains(event.target as Node)) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a') {
        event.preventDefault();
        setSelection(props.icons.map((icon) => icon.id));
      }
      if (
        event.key === 'Enter' ||
        ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'o')
      ) {
        event.preventDefault();
        props.icons
          .filter((icon) => selection.includes(icon.id))
          .forEach(openIcon);
      }
      if (
        (event.key === 'Delete' || event.key === 'Backspace') &&
        selection.length
      ) {
        event.preventDefault();
        props.onTrashIcons?.(
          selection.filter(
            (id) => !props.icons.find((icon) => icon.id === id)?.isTrash,
          ),
        );
      }
    };
    document.addEventListener('keydown', keydown);
    return () => document.removeEventListener('keydown', keydown);
  });
  const beginWindowGesture = (
    event: ReactPointerEvent,
    win: DesktopWindow,
    edge?: string,
  ) => {
    if (event.button !== 0 || mobile || overview) return;
    event.preventDefault();
    event.stopPropagation();
    activateWindow(win.id);
    const app = props.apps.find((candidate) => candidate.id === win.appId)!;
    const bounds = visibleBounds(win, viewport, app.minSize);
    const origin = { x: event.clientX, y: event.clientY };
    let next = bounds;
    let moved = false;
    beginGesture(
      event,
      (point) => {
        const delta = { x: point.x - origin.x, y: point.y - origin.y };
        if (Math.abs(delta.x) + Math.abs(delta.y) < 2 && !moved) return;
        moved = true;
        next = edge
          ? resizeBounds(
              bounds,
              delta,
              edge,
              app.minSize ?? { width: 240, height: 160 },
              viewport,
            )
          : visibleBounds(
              { ...bounds, x: bounds.x + delta.x, y: bounds.y + delta.y },
              viewport,
              app.minSize,
            );
        setWindowPreview({ [win.id]: next });
      },
      (_, cancelled) => {
        setWindowPreview({});
        if (!cancelled && moved)
          setLayout((current) => ({
            ...current,
            windows: current.windows.map((candidate) =>
              candidate.id === win.id
                ? {
                    ...candidate,
                    ...next,
                    zoomed: false,
                    restoreBounds: undefined,
                  }
                : candidate,
            ),
          }));
      },
    );
  };
  const beginIconGesture = (
    event: ReactPointerEvent<HTMLButtonElement>,
    icon: DesktopIcon,
  ) => {
    if (event.button !== 0) return;
    event.stopPropagation();
    focusDesktop();
    event.currentTarget.focus();
    const additive = event.shiftKey || event.metaKey || event.ctrlKey;
    const prior = selectionRef.current;
    const ids = additive
      ? selectionAfterClick(prior, icon.id, true)
      : prior.includes(icon.id)
        ? prior
        : [icon.id];
    setSelection(ids);
    const origin = { x: event.clientX, y: event.clientY };
    const starting = Object.fromEntries(
      props.icons.map((item, index) => [item.id, iconPosition(item, index)]),
    );
    let next: Record<string, Point> = {};
    let delta = { x: 0, y: 0 };
    let dragged = false;
    let longPressed = false;
    let overTrash = false;
    const timer =
      event.pointerType === 'touch'
        ? globalThis.setTimeout(() => {
            longPressed = true;
            contextMenu(desktopItems(ids), origin);
          }, 550)
        : undefined;
    const trash = props.icons.find((item) => item.isTrash);
    beginGesture(
      event,
      (point) => {
        const dx = point.x - origin.x,
          dy = point.y - origin.y;
        if (Math.abs(dx) + Math.abs(dy) > 6) {
          globalThis.clearTimeout(timer);
          dragged = true;
        }
        if (!dragged || longPressed) return;
        delta = { x: dx, y: dy };
        next = moveIconGroup(starting, ids, delta, viewport);
        setIconPreview(next);
        const trashPoint = trash ? starting[trash.id] : undefined;
        overTrash =
          !!trashPoint &&
          !ids.includes(trash!.id) &&
          point.x >= trashPoint.x &&
          point.x <= trashPoint.x + ICON_SIZE.width &&
          point.y >= trashPoint.y &&
          point.y <= trashPoint.y + ICON_SIZE.height;
        setTrashHover(overTrash);
      },
      (_, cancelled) => {
        globalThis.clearTimeout(timer);
        setIconPreview({});
        setTrashHover(false);
        if (cancelled) {
          setSelection(prior);
          return;
        }
        if (longPressed) return;
        if (dragged && overTrash) props.onTrashIcons?.(ids);
        else if (dragged) {
          if (props.iconSnap)
            next = moveIconGroup(starting, ids, delta, viewport, true);
          setLayout((current) => ({
            ...current,
            icons: { ...current.icons, ...next },
          }));
        } else {
          if (!additive) setSelection([icon.id]);
          if (event.pointerType === 'touch') {
            if (
              lastTouch.current?.id === icon.id &&
              Date.now() - lastTouch.current.time < 450
            ) {
              lastTouchActivation.current = { id: icon.id, time: Date.now() };
              openIcon(icon);
              lastTouch.current = null;
            } else lastTouch.current = { id: icon.id, time: Date.now() };
          }
        }
      },
    );
  };
  const beginMarquee = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget || event.button !== 0) return;
    focusDesktop();
    event.currentTarget.focus();
    const origin = { x: event.clientX, y: event.clientY };
    const additive = event.metaKey || event.ctrlKey || event.shiftKey;
    const previous = selection;
    const initial = additive ? previous : [];
    setSelection(initial);
    let moved = false;
    let longPressed = false;
    const timer =
      event.pointerType === 'touch'
        ? globalThis.setTimeout(() => {
            longPressed = true;
            setMarquee(null);
            contextMenu(desktopItems(initial), origin);
          }, 550)
        : undefined;
    beginGesture(
      event,
      (point) => {
        if (Math.abs(point.x - origin.x) + Math.abs(point.y - origin.y) > 6) {
          moved = true;
          globalThis.clearTimeout(timer);
        }
        if (!moved || longPressed) return;
        const rectangle = marqueeBounds(origin, point);
        setMarquee(rectangle);
        const selected = props.icons
          .filter((icon, index) =>
            intersects(rectangle, {
              ...iconPosition(icon, index),
              ...ICON_SIZE,
            }),
          )
          .map((icon) => icon.id);
        setSelection([...new Set([...initial, ...selected])]);
      },
      (_, cancelled) => {
        globalThis.clearTimeout(timer);
        setMarquee(null);
        if (cancelled) setSelection(previous);
      },
    );
  };
  const doubleClickIcon = (icon: DesktopIcon) => {
    if (
      lastTouchActivation.current?.id === icon.id &&
      Date.now() - lastTouchActivation.current.time < 700
    )
      return;
    openIcon(icon);
  };
  return {
    selection,
    setSelection,
    windowPreview,
    marquee,
    trashHover,
    setTrashHover,
    desktopRef,
    iconPosition,
    beginWindowGesture,
    beginIconGesture,
    beginMarquee,
    doubleClickIcon,
    focusDesktop,
    desktopItems,
    contextMenu,
  };
}

export type DesktopGestures = ReturnType<typeof useDesktopGestures>;
