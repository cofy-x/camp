import { describe, expect, it } from 'vitest';
import {
  initialIconPosition,
  intersects,
  marqueeBounds,
  mergeMenuGroups,
  moveIconGroup,
  overviewTransform,
  resizeBounds,
  sanitizeLayout,
  selectionAfterClick,
  visibleBounds,
  windowIdentity,
} from './model';
import type { AppDefinition, DesktopLayout } from './types';

const app: AppDefinition = {
  id: 'files',
  title: 'Files',
  icon: 'folder',
  component: () => null,
  windowKey: (params) => params.folder ?? 'documents',
};
const bounds = { x: 70, y: 80, width: 500, height: 400 };
const viewport = { width: 1024, height: 768 };

describe('application menus', () => {
  it('replaces built-in menus and inserts app menus without duplicate IDs', () => {
    const defaults = ['desktop', 'file', 'view', 'window'].map((id) => ({
      id,
      label: id,
      items: [],
    }));
    const file = {
      id: 'file',
      label: 'File',
      items: [{ label: 'New Folder…' }],
    };
    const result = mergeMenuGroups(defaults, [
      file,
      { id: 'edit', label: 'Edit', items: [] },
    ]);
    expect(result.map((group) => group.id)).toEqual([
      'desktop',
      'file',
      'edit',
      'view',
      'window',
    ]);
    expect(result[1]).toEqual(file);
    expect(defaults.map((group) => group.id)).toEqual([
      'desktop',
      'file',
      'view',
      'window',
    ]);
  });
});

describe('window geometry and restoration', () => {
  it('makes per-folder windows stable and distinct', () => {
    expect(windowIdentity(app)).toBe('files:documents');
    expect(windowIdentity(app, { folder: 'pictures' })).toBe('files:pictures');
  });
  it('keeps title bars and content reachable when a display shrinks without mutating saved bounds', () => {
    const saved = { ...bounds, x: 1300, y: 900 };
    const result = visibleBounds(saved, { width: 390, height: 844 });
    expect(result.x).toBeGreaterThanOrEqual(4);
    expect(result.y).toBeGreaterThanOrEqual(26);
    expect(result.x + result.width).toBeLessThanOrEqual(390);
    expect(result.y + result.height).toBeLessThanOrEqual(844 - 24);
    expect(saved).toEqual({ ...bounds, x: 1300, y: 900 });
  });
  it('keeps windows and desktop icons outside enlarged touch chrome', () => {
    const touchViewport = { ...viewport, menuHeight: 34, stripHeight: 34 };
    const saved = { ...bounds, y: 0, height: 1000 };
    const result = visibleBounds(saved, touchViewport);
    expect(result.y).toBeGreaterThanOrEqual(touchViewport.menuHeight + 6);
    expect(result.y + result.height).toBeLessThanOrEqual(
      touchViewport.height - touchViewport.stripHeight - 8,
    );
    const resized = resizeBounds(
      result,
      { x: 0, y: -1000 },
      'n',
      { width: 240, height: 160 },
      touchViewport,
    );
    expect(resized.y).toBeGreaterThanOrEqual(touchViewport.menuHeight + 6);
    const positions = { icon: initialIconPosition(0, touchViewport) };
    const moved = moveIconGroup(
      positions,
      ['icon'],
      { x: 0, y: -1000 },
      touchViewport,
    );
    expect(moved.icon.y).toBeGreaterThanOrEqual(touchViewport.menuHeight + 6);
    expect(saved.y).toBe(0);
    expect(saved.height).toBe(1000);
  });
  it('limits all eight resize directions and keeps minimum dimensions', () => {
    const minimum = { width: 240, height: 160 };
    for (const edge of ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']) {
      const result = resizeBounds(
        bounds,
        { x: 2000, y: 2000 },
        edge,
        minimum,
        viewport,
      );
      expect(result.width).toBeGreaterThanOrEqual(minimum.width);
      expect(result.height).toBeGreaterThanOrEqual(minimum.height);
      expect(result.x).toBeGreaterThanOrEqual(4);
      expect(result.x + result.width).toBeLessThanOrEqual(viewport.width);
      expect(result.y + result.height).toBeLessThanOrEqual(
        viewport.height - 24,
      );
    }
  });
  it('resizing from the west preserves the east edge at the minimum width', () => {
    const result = resizeBounds(
      bounds,
      { x: 490, y: 0 },
      'w',
      { width: 240, height: 160 },
      viewport,
    );
    expect(result.width).toBe(240);
    expect(result.x + result.width).toBe(bounds.x + bounds.width);
  });
  it('overview derives transforms and never changes persisted geometry', () => {
    const saved = structuredClone(bounds);
    expect(overviewTransform(bounds, 2, 5, viewport)).toMatch(
      /^translate\(.+\) scale\(.+\)$/,
    );
    expect(bounds).toEqual(saved);
  });
  it('filters stale app references and malformed geometry from stored layouts', () => {
    const layout: DesktopLayout = {
      version: 1,
      activeWindowId: 'missing',
      icons: { good: { x: 20, y: 40 }, bad: { x: NaN, y: 20 } },
      windows: [
        {
          ...bounds,
          id: 'files:documents',
          appId: 'files',
          title: 'Documents',
          params: {},
          shaded: false,
          zoomed: false,
        },
        {
          ...bounds,
          id: 'old:main',
          appId: 'old',
          title: 'Removed app',
          params: {},
          shaded: false,
          zoomed: false,
        },
        {
          ...bounds,
          x: Infinity,
          id: 'files:invalid',
          appId: 'files',
          title: 'Invalid',
          params: {},
          shaded: false,
          zoomed: false,
        },
      ],
    };
    const restored = sanitizeLayout(layout, [app]);
    expect(restored.windows).toHaveLength(1);
    expect(restored.activeWindowId).toBe('files:documents');
    expect(restored.icons).toEqual({ good: { x: 20, y: 40 } });
    const desktopFocused = sanitizeLayout(
      { ...restored, activeWindowId: null },
      [app],
    );
    expect(desktopFocused.activeWindowId).toBeNull();
    expect(desktopFocused.windows).toEqual(restored.windows);
  });
});

describe('desktop selection', () => {
  it('supports replacement, additive selection, and toggling selected icons off', () => {
    expect(selectionAfterClick(['a', 'b'], 'c', false)).toEqual(['c']);
    expect(selectionAfterClick(['a'], 'b', true)).toEqual(['a', 'b']);
    expect(selectionAfterClick(['a', 'b'], 'a', true)).toEqual(['b']);
  });
  it('selects intersections in either marquee direction', () => {
    const box = marqueeBounds({ x: 300, y: 300 }, { x: 100, y: 100 });
    expect(box).toEqual({ x: 100, y: 100, width: 200, height: 200 });
    expect(intersects(box, { x: 110, y: 120, width: 88, height: 74 })).toBe(
      true,
    );
    expect(intersects(box, { x: 350, y: 120, width: 88, height: 74 })).toBe(
      false,
    );
  });
  it('arranges icons in columns and keeps mobile icons in the viewport', () => {
    for (let index = 0; index < 8; index++) {
      const point = initialIconPosition(index, { width: 390, height: 844 });
      expect(point.x).toBeGreaterThanOrEqual(8);
      expect(point.x + 88).toBeLessThanOrEqual(390);
      expect(point.y + 74).toBeLessThanOrEqual(844);
    }
  });
  it('keeps a dragged group spaced apart at every viewport edge and when snapping', () => {
    const positions = { first: { x: 100, y: 100 }, second: { x: 190, y: 174 } };
    for (const delta of [
      { x: 2000, y: 2000 },
      { x: -2000, y: -2000 },
      { x: 23, y: 11 },
    ]) {
      for (const snap of [false, true]) {
        const result = moveIconGroup(
          positions,
          ['first', 'second'],
          delta,
          viewport,
          snap,
        );
        expect(result.second.x - result.first.x).toBe(90);
        expect(result.second.y - result.first.y).toBe(74);
        expect(result.first.x).toBeGreaterThanOrEqual(0);
        expect(result.first.y).toBeGreaterThanOrEqual(26);
        expect(result.second.x + 88).toBeLessThanOrEqual(viewport.width);
        expect(result.second.y + 74).toBeLessThanOrEqual(viewport.height - 32);
      }
    }
    expect(positions).toEqual({
      first: { x: 100, y: 100 },
      second: { x: 190, y: 174 },
    });
    expect(
      moveIconGroup(positions, ['missing'], { x: 10, y: 10 }, viewport),
    ).toEqual({});
  });
});
