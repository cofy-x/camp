import { createContext, useContext } from 'react';

// Hidden windows retain their local state; readers can suspend their work.
export const WindowVisibility = createContext(true);

export function useWindowVisibility() {
  return useContext(WindowVisibility);
}
