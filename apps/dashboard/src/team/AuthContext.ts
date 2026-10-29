import { createContext, useContext } from 'react';
import type { User } from './api';

export const AuthContext = createContext<{
  user: User;
  signOut: () => Promise<void>;
} | null>(null);

export function useAccount() {
  const account = useContext(AuthContext);
  if (!account) throw new Error('An authenticated account is required.');
  return account;
}
