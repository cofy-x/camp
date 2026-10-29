import type {
  AppDefinition,
  Bounds,
  DesktopLayout,
  DesktopWindow,
  DesktopViewport,
  MenuGroup,
  Point,
  Size,
  WindowParams,
} from './types';

export const MENU_HEIGHT = 20;
export const STRIP_HEIGHT = 24;
export const ICON_SIZE = { width: 88, height: 74 };

/** Applications replace matching menus and insert additional menus after their preceding contribution. */
export function mergeMenuGroups(
  defaults: MenuGroup[],
  contributions: MenuGroup[],
): MenuGroup[] {
  const groups = [...defaults];
  let insertion = Math.max(
    0,
    groups.findIndex((group) => group.id === 'window'),
  );
  if (!insertion) insertion = groups.length;
  for (const group of contributions) {
    const existing = groups.findIndex((candidate) => candidate.id === group.id);
    if (existing >= 0) {
      groups[existing] = group;
      insertion = existing + 1;
    } else {
      groups.splice(insertion, 0, group);
      insertion += 1;
    }
  }
  return groups;
}

export function windowIdentity(
  app: AppDefinition,
  params: WindowParams = {},
): string {
  return `${app.id}:${app.windowKey?.(params) ?? 'main'}`;
}

/** Keep every title bar reachable without destroying a stored large-screen layout. */
export function visibleBounds(
  bounds: Bounds,
  viewport: DesktopViewport,
  minSize: Size = { width: 240, height: 160 },
): Bounds {
  const menuHeight = viewport.menuHeight ?? MENU_HEIGHT;
  const stripHeight = viewport.stripHeight ?? STRIP_HEIGHT;
  const availableWidth = Math.max(1, viewport.width - 8);
  const availableHeight = Math.max(
    1,
    viewport.height - menuHeight - stripHeight - 16,
  );
  const width = Math.min(availableWidth, Math.max(minSize.width, bounds.width));
  const height = Math.min(
    availableHeight,
    Math.max(minSize.height, bounds.height),
  );
  return {
    x: Math.round(
      Math.min(Math.max(4, bounds.x), Math.max(4, viewport.width - width - 4)),
    ),
    y: Math.round(
      Math.min(
        Math.max(menuHeight + 6, bounds.y),
        Math.max(menuHeight + 6, viewport.height - height - stripHeight - 8),
      ),
    ),
    width: Math.round(width),
    height: Math.round(height),
  };
}

export function resizeBounds(
  bounds: Bounds,
  delta: Point,
  edge: string,
  minSize: Size,
  viewport: DesktopViewport,
): Bounds {
  const next = { ...bounds };
  if (edge.includes('e')) next.width += delta.x;
  if (edge.includes('s')) next.height += delta.y;
  if (edge.includes('w')) {
    next.x += delta.x;
    next.width -= delta.x;
  }
  if (edge.includes('n')) {
    next.y += delta.y;
    next.height -= delta.y;
  }
  if (next.width < minSize.width) {
    if (edge.includes('w')) next.x -= minSize.width - next.width;
    next.width = minSize.width;
  }
  if (next.height < minSize.height) {
    if (edge.includes('n')) next.y -= minSize.height - next.height;
    next.height = minSize.height;
  }
  return visibleBounds(next, viewport, minSize);
}

export function selectionAfterClick(
  current: string[],
  id: string,
  additive: boolean,
): string[] {
  if (!additive) return [id];
  return current.includes(id)
    ? current.filter((item) => item !== id)
    : [...current, id];
}

export function intersects(a: Bounds, b: Bounds): boolean {
  return (
    a.x < b.x + b.width &&
    a.x + a.width > b.x &&
    a.y < b.y + b.height &&
    a.y + a.height > b.y
  );
}

export function marqueeBounds(start: Point, end: Point): Bounds {
  return {
    x: Math.min(start.x, end.x),
    y: Math.min(start.y, end.y),
    width: Math.abs(end.x - start.x),
    height: Math.abs(end.y - start.y),
  };
}

/** Clamp one shared movement vector so selected icons retain their spacing. */
export function moveIconGroup(
  positions: Record<string, Point>,
  ids: string[],
  delta: Point,
  viewport: DesktopViewport,
  snap = false,
): Record<string, Point> {
  const selected = ids.filter((id) => positions[id]);
  if (!selected.length) return {};
  const points = selected.map((id) => positions[id]);
  const anchor = points[0];
  const dx = snap
    ? Math.round((anchor.x + delta.x) / 8) * 8 - anchor.x
    : delta.x;
  const dy = snap
    ? Math.round((anchor.y + delta.y) / 8) * 8 - anchor.y
    : delta.y;
  const x = Math.min(
    Math.max(dx, -Math.min(...points.map((point) => point.x))),
    viewport.width -
      ICON_SIZE.width -
      Math.max(...points.map((point) => point.x)),
  );
  const y = Math.min(
    Math.max(
      dy,
      (viewport.menuHeight ?? MENU_HEIGHT) +
        6 -
        Math.min(...points.map((point) => point.y)),
    ),
    viewport.height -
      ICON_SIZE.height -
      (viewport.stripHeight ?? STRIP_HEIGHT) -
      8 -
      Math.max(...points.map((point) => point.y)),
  );
  return Object.fromEntries(
    selected.map((id) => [
      id,
      { x: positions[id].x + x, y: positions[id].y + y },
    ]),
  );
}

export function initialIconPosition(
  index: number,
  viewport: DesktopViewport,
): Point {
  const menuHeight = viewport.menuHeight ?? MENU_HEIGHT;
  const stripHeight = viewport.stripHeight ?? STRIP_HEIGHT;
  const rows = Math.max(
    1,
    Math.floor(
      (viewport.height - menuHeight - stripHeight - 30) / ICON_SIZE.height,
    ),
  );
  return {
    x: Math.max(8, viewport.width - 100 - Math.floor(index / rows) * 94),
    y: menuHeight + 20 + (index % rows) * ICON_SIZE.height,
  };
}

export function overviewTransform(
  bounds: Bounds,
  index: number,
  count: number,
  viewport: DesktopViewport,
): string {
  const columns = Math.max(
    1,
    Math.ceil(Math.sqrt((count * viewport.width) / viewport.height)),
  );
  const rows = Math.ceil(count / columns);
  const cellWidth = (viewport.width - 24) / columns;
  const menuHeight = viewport.menuHeight ?? MENU_HEIGHT;
  const stripHeight = viewport.stripHeight ?? STRIP_HEIGHT;
  const cellHeight = (viewport.height - menuHeight - stripHeight - 36) / rows;
  const scale = Math.min(
    (cellWidth - 20) / bounds.width,
    (cellHeight - 20) / bounds.height,
    1,
  );
  const x =
    12 + (index % columns) * cellWidth + (cellWidth - bounds.width * scale) / 2;
  const y =
    menuHeight +
    14 +
    Math.floor(index / columns) * cellHeight +
    (cellHeight - bounds.height * scale) / 2;
  return `translate(${Math.round(x - bounds.x)}px, ${Math.round(y - bounds.y)}px) scale(${Math.max(0.1, scale)})`;
}

export function sanitizeLayout(
  layout: DesktopLayout | undefined,
  apps: AppDefinition[],
): DesktopLayout {
  const valid = layout?.version === 1 && Array.isArray(layout.windows);
  const windows: DesktopWindow[] = [];
  if (valid)
    for (const candidate of layout.windows) {
      if (
        !candidate ||
        !apps.some((app) => app.id === candidate.appId) ||
        typeof candidate.id !== 'string' ||
        windows.some((win) => win.id === candidate.id)
      )
        continue;
      if (
        ![candidate.x, candidate.y, candidate.width, candidate.height].every(
          Number.isFinite,
        )
      )
        continue;
      windows.push({
        ...candidate,
        title: String(candidate.title || candidate.appId),
        params: Object.fromEntries(
          Object.entries(candidate.params || {}).filter(
            ([, value]) => typeof value === 'string',
          ),
        ),
        shaded: Boolean(candidate.shaded),
        zoomed: Boolean(candidate.zoomed),
      });
    }
  const icons = Object.fromEntries(
    Object.entries(layout?.icons || {}).filter(
      ([, point]) =>
        point && Number.isFinite(point.x) && Number.isFinite(point.y),
    ),
  );
  return {
    version: 1,
    windows,
    icons,
    activeWindowId:
      layout?.activeWindowId === null
        ? null
        : windows.some((win) => win.id === layout?.activeWindowId)
          ? layout!.activeWindowId
          : (windows.at(-1)?.id ?? null),
  };
}
