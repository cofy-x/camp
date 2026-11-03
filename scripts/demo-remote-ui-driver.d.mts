import type { Locator, Page } from '@playwright/test';
import type { Agent, Room } from '../apps/dashboard/src/features/workspace/api';
import type {
  RunnerSource,
  SourceImport,
} from '../apps/dashboard/src/features/runners/api';

type Observe = (route: string) => Promise<unknown>;
type Snapshot = (caption: string) => Promise<void>;

export class DemoError extends Error {}
export function check(value: unknown, message: string): asserts value;
export function openDemoApp(page: Page, name: string): Promise<void>;
export function selectDemoValue(
  page: Page,
  scope: Locator,
  name: string,
  value: string,
): Promise<void>;
export function importDemoRoom(input: {
  page: Page;
  get: Observe;
  runnerId: string;
  source: RunnerSource;
  roomName: string;
  snapshot: Snapshot;
}): Promise<{ room: Room; imported: SourceImport & { commit: string } }>;
export function createDemoAgent(input: {
  page: Page;
  get: Observe;
  room: Room;
  user: { id: string };
  runnerId: string;
  name: string;
  snapshot: Snapshot;
}): Promise<Agent>;
export function prepareDemoComposer(
  page: Page,
  room: Room,
  agent: Agent,
  prompt: string,
): Promise<Locator>;

export interface DemoRequest {
  route: string;
  prompt: string;
  runnerRevision: number;
  agentId: string;
  feedbackMessageId?: string;
  intentId: string;
  reservedAt: string;
  state: string;
  body?: unknown;
  run?: { status: string; published?: boolean };
}

export interface DemoLedger {
  origin: string;
  allowedRuns: number;
  requests: DemoRequest[];
}

export function installDemoDispatchGuard(input: {
  page: Page;
  ledger: DemoLedger;
  save: () => Promise<void>;
}): Promise<{
  reserve(
    intent: Omit<
      DemoRequest,
      'intentId' | 'reservedAt' | 'state' | 'body' | 'run'
    >,
  ): DemoRequest;
  disarm(): void;
  blocked(): boolean;
}>;
