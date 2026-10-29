import { useEffect, useState } from 'react';
import { MENU_HEIGHT, STRIP_HEIGHT } from '../model';
import type { DesktopViewport } from '../types';

export const viewportSize = (): DesktopViewport => {
  const styles = getComputedStyle(document.documentElement);
  return {
    width: globalThis.innerWidth || 1024,
    height: globalThis.visualViewport?.height || globalThis.innerHeight || 768,
    menuHeight:
      Number.parseFloat(styles.getPropertyValue('--p9-menubar-height')) ||
      MENU_HEIGHT,
    stripHeight:
      Number.parseFloat(styles.getPropertyValue('--p9-control-strip-height')) ||
      STRIP_HEIGHT,
  };
};

export function useViewport() {
  const [viewport, setViewport] = useState(viewportSize);
  useEffect(() => {
    const resize = () => setViewport(viewportSize());
    const pointer = globalThis.matchMedia('(pointer: coarse)');
    globalThis.addEventListener('resize', resize);
    pointer.addEventListener('change', resize);
    globalThis.visualViewport?.addEventListener('resize', resize);
    return () => {
      globalThis.removeEventListener('resize', resize);
      pointer.removeEventListener('change', resize);
      globalThis.visualViewport?.removeEventListener('resize', resize);
    };
  }, []);
  return viewport;
}
