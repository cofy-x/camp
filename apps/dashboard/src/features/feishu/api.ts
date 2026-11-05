import { request } from '../../shared/api/http';

export interface FeishuSource {
  id: string;
  kind: 'feishu';
  roomId: string;
  appId: string;
  tenantKey: string;
  chatId: string;
  enabled: boolean;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

interface SourceReceipt {
  kind: string;
  source?: FeishuSource;
}

export interface FeishuSourceInput {
  requestId: string;
  appId: string;
  tenantKey: string;
  chatId: string;
  verificationToken: string;
  encryptKey: string;
}

const id = encodeURIComponent;
const json = (method: string, body: unknown) => ({
  method,
  body: JSON.stringify(body),
});
export const feishuApi = {
  sources: (room: string, signal?: AbortSignal) =>
    request<{ sources: FeishuSource[] }>(`/rooms/${id(room)}/feishu-sources`, {
      signal,
    }),
  create: (room: string, input: FeishuSourceInput) =>
    request<{ source: FeishuSource }>(
      `/rooms/${id(room)}/feishu-sources`,
      json('POST', input),
    ).then(({ source }) => ({ kind: 'source', source })),
  update: (
    source: string,
    input: {
      requestId: string;
      revision: number;
      enabled?: boolean;
      verificationToken?: string;
      encryptKey?: string;
    },
  ) =>
    request<{ source: FeishuSource }>(
      `/feishu-sources/${id(source)}`,
      json('PATCH', input),
    ).then(({ source }) => ({ kind: 'source', source })),
  receipt: (requestId: string) =>
    request<SourceReceipt>(`/feishu-requests/${id(requestId)}`),
  cancelReceipt: (requestId: string) =>
    request<SourceReceipt>(
      `/feishu-requests/${id(requestId)}/cancel`,
      json('POST', {}),
    ),
  payloadURL: (eventId: string) => `/api/feishu-events/${id(eventId)}/payload`,
  hookURL: (sourceId: string) =>
    `${location.origin}/api/hooks/feishu/${id(sourceId)}`,
};
