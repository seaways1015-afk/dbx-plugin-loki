export type Context = { connectionId?: string; connectionName?: string; [key: string]: unknown };

export type Theme = { appearance?: 'light' | 'dark'; tokens?: Record<string, string>; editor?: { fontSize?: number; fontFamily?: string } };

export type DbxPluginApi = {
  ready: Promise<unknown>;
  context: Context;
  locale: string;
  theme?: Theme;
  capabilities?: { storage?: boolean; ai?: boolean; clipboardWrite?: boolean; [key: string]: unknown };
  invoke<T>(method: string, params: Record<string, unknown>, options?: { timeoutMs: number }): Promise<T>;
  onContext(fn: (context: Context) => void): () => void;
  setTitle?(title: string | null): Promise<void>;
  storage?: {
    get<T>(key: string): Promise<T | null>;
    set(key: string, value: unknown): Promise<void>;
  };
  clipboard?: { writeText(text: string): Promise<void> };
  ai?: { openConversation(options: { title: string; prompt: string; context?: unknown; send?: boolean; mode?: 'ask' | 'agent' }): Promise<void> };
};

declare global {
  interface Window {
    dbxPlugin?: DbxPluginApi;
  }
}

export type LogEntry = { ts: string; line: string; labels: Record<string, string>; stream: number; truncated?: boolean };
export type SamplePoint = { t: number; v: string };
export type MetricSerie = { labels: Record<string, string>; points: SamplePoint[] };
export type QueryResult = {
  resultType: 'streams' | 'matrix' | 'vector' | 'scalar';
  entries?: LogEntry[];
  series?: MetricSerie[];
  scalar?: SamplePoint;
  truncated: boolean;
  stats: { streams: number; entries: number; bytesProcessed: number; execTimeSeconds: number };
};
export type HistogramResult = { step: number; buckets: SamplePoint[]; total: number };
export type QueryForm = {
  query?: string;
  label?: string;
  start?: string;
  end?: string;
  since?: string;
  limit?: number;
  direction?: 'backward' | 'forward';
  step?: number;
};

export function plugin(): DbxPluginApi {
  if (!window.dbxPlugin) throw new Error('Open this workbench from a Loki connection in DBX.');
  return window.dbxPlugin;
}

export function call<T>(connectionId: string, method: string, form: QueryForm = {}, timeoutMs = 95_000): Promise<T> {
  return plugin().invoke<T>(method, { connectionId, form }, { timeoutMs });
}

export async function loadState<T>(key: string): Promise<T | null> {
  const api = window.dbxPlugin;
  if (!api?.capabilities?.storage || !api.storage) return null;
  try {
    return await api.storage.get<T>(key);
  } catch {
    return null;
  }
}

export async function saveState(key: string, value: unknown): Promise<void> {
  const api = window.dbxPlugin;
  if (!api?.capabilities?.storage || !api.storage) return;
  try {
    await api.storage.set(key, value);
  } catch {
    // UI state is best effort.
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'object' && error && 'message' in error) return String((error as { message: unknown }).message);
  return String(error);
}
