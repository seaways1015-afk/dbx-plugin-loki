// Local preview host: `npm run dev` then open /preview.html. Emulates the DBX
// bridge with generated logs so the UI can be developed without DBX or Loki.
// This module is only referenced by preview.html and never ships in ui/.
import type { DbxPluginApi, QueryForm } from './bridge';

const apps = ['api', 'web', 'worker', 'gateway'];
const namespaces = ['prod', 'staging'];
const messages = [
  ['info', 'GET /api/orders/{id} 200 12ms'],
  ['info', 'POST /api/checkout 201 87ms'],
  ['warn', 'slow query detected duration=1.8s table=orders'],
  ['error', 'failed to charge card: upstream timeout after 30s'],
  ['debug', 'cache miss key=user:42'],
  ['error', 'panic: runtime error: index out of range [3] with length 3'],
  ['info', 'worker heartbeat ok queue=emails depth=12'],
  ['warn', 'retrying request attempt=2 err="connection reset by peer"'],
] as const;

function generate(startMs: number, endMs: number, count: number) {
  const out = [];
  for (let i = 0; i < count; i += 1) {
    const ts = endMs - ((endMs - startMs) * i) / count - Math.random() * 1000;
    const [level, msg] = messages[Math.floor(Math.random() * messages.length)];
    const app = apps[i % apps.length];
    out.push({
      ts: (BigInt(Math.floor(ts)) * 1_000_000n + BigInt(i % 1000)).toString(),
      line: `level=${level} ts=${new Date(ts).toISOString()} caller=${app}/handler.go:${100 + (i % 80)} msg="${msg}"`,
      labels: { app, namespace: namespaces[i % 2], level },
      stream: i % 8,
    });
  }
  return out;
}

const range = (form: QueryForm) => {
  const end = form.end ? Number(BigInt(form.end) / 1_000_000n) : Date.now();
  const start = form.start ? Number(BigInt(form.start) / 1_000_000n) : end - 3600_000;
  return { start, end };
};

const handlers: Record<string, (form: QueryForm) => unknown> = {
  'loki/labels': () => ({ labels: ['app', 'level', 'namespace'] }),
  'loki/label_values': form => ({ values: form.label === 'app' ? apps : form.label === 'namespace' ? namespaces : ['debug', 'error', 'info', 'warn'], truncated: false }),
  'loki/query_range': form => {
    if (form.query?.includes('{') && /^(sum|rate|count_over_time)/.test(form.query)) {
      const { start, end } = range(form);
      const step = (end - start) / 60;
      return {
        resultType: 'matrix',
        series: apps.map(app => ({ labels: { app }, points: Array.from({ length: 60 }, (_, i) => ({ t: (start + i * step) / 1000, v: String(Math.round(20 + Math.random() * 40)) })) })),
        truncated: false,
        stats: { streams: 4, entries: 0, bytesProcessed: 2_400_000, execTimeSeconds: 0.08 },
      };
    }
    const { start, end } = range(form);
    const entries = generate(start, end, Math.min(form.limit ?? 500, 800));
    if (form.direction === 'forward') entries.reverse();
    return { resultType: 'streams', entries, truncated: false, stats: { streams: 8, entries: entries.length, bytesProcessed: 18_400_000, execTimeSeconds: 0.142 } };
  },
  'loki/histogram': form => {
    const { start, end } = range(form);
    const stepS = Math.max(1, Math.round((end - start) / 1000 / 60));
    const buckets = Array.from({ length: 60 }, (_, i) => ({ t: start / 1000 + (i + 1) * stepS, v: String(Math.round(30 + 25 * Math.sin(i / 6) + Math.random() * 20 + (i === 41 ? 120 : 0))) }));
    return { step: stepS, buckets, total: buckets.reduce((sum, bucket) => sum + Number(bucket.v), 0) };
  },
  'loki/tail': () => {
    const now = Date.now();
    return { resultType: 'streams', entries: generate(now - 2000, now, 1 + Math.floor(Math.random() * 4)).reverse(), truncated: false, stats: { streams: 1, entries: 1, bytesProcessed: 0, execTimeSeconds: 0 } };
  },
};

const store = new Map<string, unknown>();

const api: DbxPluginApi = {
  ready: Promise.resolve(),
  context: { connectionId: 'preview' },
  locale: navigator.language,
  theme: { appearance: matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light' },
  capabilities: { storage: true, ai: false },
  async invoke<T>(method: string, params: Record<string, unknown>) {
    await new Promise(resolve => setTimeout(resolve, 150));
    const handler = handlers[method];
    if (!handler) throw new Error(`Preview has no handler for ${method}`);
    return handler((params.form ?? {}) as QueryForm) as T;
  },
  onContext: () => () => undefined,
  storage: {
    async get<T>(key: string) { return (store.get(key) as T) ?? null; },
    async set(key: string, value: unknown) { store.set(key, value); },
  },
};

(window as { __LOKI_PREVIEW__?: boolean }).__LOKI_PREVIEW__ = true;
window.dbxPlugin = api;
