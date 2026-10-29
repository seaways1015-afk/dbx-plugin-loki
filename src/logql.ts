// Pure helpers shared by the workbench components. Kept free of Vue and DOM
// dependencies so they can be unit tested.

export type RangePreset = { id: string; since: string; seconds: number };

export const rangePresets: RangePreset[] = [
  { id: '5m', since: '5m', seconds: 300 },
  { id: '15m', since: '15m', seconds: 900 },
  { id: '1h', since: '1h', seconds: 3600 },
  { id: '6h', since: '6h', seconds: 21600 },
  { id: '24h', since: '24h', seconds: 86400 },
  { id: '7d', since: '7d', seconds: 604800 },
];

const nsPerMs = 1_000_000n;

export function msToNs(ms: number): string {
  return (BigInt(Math.floor(ms)) * nsPerMs).toString();
}

export function nsToMs(ns: string): number {
  return Number(BigInt(ns) / nsPerMs);
}

export function nextNs(ns: string): string {
  return (BigInt(ns) + 1n).toString();
}

export function compareNs(a: string, b: string): number {
  if (a.length !== b.length) return a.length - b.length;
  return a < b ? -1 : a > b ? 1 : 0;
}

// Live tail polls re-read the last liveOverlapMs before the newest line seen,
// because agents that batch pushes (Promtail, Alloy) deliver lines whose
// timestamps are older than lines other agents already delivered. Lines that
// were already shown are skipped by identity.
export const liveOverlapMs = 10_000;

type LiveEntry = { ts: string; line: string; labels: Record<string, string> };

/** Identity of a log line across responses: timestamp, stream labels and text. */
export function entryKey(entry: LiveEntry): string {
  const labels = Object.keys(entry.labels).sort().map(key => `${key}=${entry.labels[key]}`).join(',');
  return `${entry.ts}\u0000${labels}\u0000${entry.line}`;
}

export class LiveCursor {
  private newest: string;
  private readonly seen = new Map<string, string>();

  constructor(newest: string, shown: LiveEntry[] = [], private readonly overlapNs = BigInt(liveOverlapMs) * nsPerMs) {
    this.newest = newest;
    this.accept(shown);
  }

  /** Start of the next poll window, in nanoseconds. */
  get start(): string {
    const start = BigInt(this.newest) - this.overlapNs;
    return (start > 0n ? start : 1n).toString();
  }

  /** Returns the entries not seen before, in input order, and advances the cursor. */
  accept<T extends LiveEntry>(entries: T[]): T[] {
    const fresh: T[] = [];
    for (const entry of entries) {
      const key = entryKey(entry);
      if (this.seen.has(key)) continue;
      this.seen.set(key, entry.ts);
      fresh.push(entry);
      if (compareNs(entry.ts, this.newest) > 0) this.newest = entry.ts;
    }
    const floor = this.start;
    for (const [key, ts] of this.seen) if (compareNs(ts, floor) < 0) this.seen.delete(key);
    return fresh;
  }
}

function pad(value: number, size = 2): string {
  return String(value).padStart(size, '0');
}

export function formatTimestamp(ns: string, utc: boolean): string {
  const ms = nsToMs(ns);
  const date = new Date(ms);
  const sub = ns.length > 6 ? ns.slice(-9, -6) : '000';
  const parts = utc
    ? [date.getUTCFullYear(), date.getUTCMonth() + 1, date.getUTCDate(), date.getUTCHours(), date.getUTCMinutes(), date.getUTCSeconds()]
    : [date.getFullYear(), date.getMonth() + 1, date.getDate(), date.getHours(), date.getMinutes(), date.getSeconds()];
  return `${parts[0]}-${pad(parts[1])}-${pad(parts[2])} ${pad(parts[3])}:${pad(parts[4])}:${pad(parts[5])}.${sub}`;
}

/** Evenly spaced tick times (ms) from `fromMs` to `toMs` inclusive. */
export function axisTicks(fromMs: number, toMs: number, count = 5): number[] {
  if (toMs <= fromMs || count < 2) return [fromMs];
  return Array.from({ length: count }, (_, i) => fromMs + ((toMs - fromMs) * i) / (count - 1));
}

/** Axis label: seconds for spans under 2 minutes, the date for spans over a day. */
export function formatAxisTime(ms: number, spanMs: number, utc: boolean): string {
  const date = new Date(ms);
  const [month, day, hours, minutes, seconds] = utc
    ? [date.getUTCMonth() + 1, date.getUTCDate(), date.getUTCHours(), date.getUTCMinutes(), date.getUTCSeconds()]
    : [date.getMonth() + 1, date.getDate(), date.getHours(), date.getMinutes(), date.getSeconds()];
  const time = spanMs < 120_000 ? `${pad(hours)}:${pad(minutes)}:${pad(seconds)}` : `${pad(hours)}:${pad(minutes)}`;
  return spanMs > 86_400_000 ? `${pad(month)}-${pad(day)} ${time}` : time;
}

export function formatBytes(bytes: number): string {
  if (!bytes) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const exponent = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exponent;
  return `${value >= 10 || exponent === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[exponent]}`;
}

export function escapeLabelValue(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n');
}

// Matches the leading stream selector of a LogQL query: `{...}`.
const selectorPattern = /^\s*\{([^}]*)\}/;

/**
 * Adds (or replaces) `label="value"` in the leading stream selector of a
 * query. Queries without a selector get a new one; the rest of the pipeline
 * is preserved.
 */
export function addLabelMatcher(query: string, label: string, value: string, operator: '=' | '!=' = '='): string {
  const matcher = `${label}${operator}"${escapeLabelValue(value)}"`;
  const match = query.match(selectorPattern);
  if (!match) {
    const rest = query.trim();
    return rest ? `{${matcher}} ${rest}` : `{${matcher}}`;
  }
  const matchers = splitMatchers(match[1]).filter(item => !new RegExp(`^${label}\\s*(=|!=|=~|!~)`).test(item));
  matchers.push(matcher);
  return query.replace(selectorPattern, `{${matchers.join(', ')}}`);
}

/** Splits selector matchers on commas that are not inside quoted values. */
export function splitMatchers(body: string): string[] {
  const items: string[] = [];
  let current = '';
  let quoted = false;
  for (let index = 0; index < body.length; index += 1) {
    const char = body[index];
    if (char === '\\' && quoted) {
      current += char + (body[index + 1] ?? '');
      index += 1;
      continue;
    }
    if (char === '"' || char === '`') quoted = !quoted;
    if (char === ',' && !quoted) {
      if (current.trim()) items.push(current.trim());
      current = '';
      continue;
    }
    current += char;
  }
  if (current.trim()) items.push(current.trim());
  return items;
}

export function selectorOf(query: string): string {
  const match = query.match(selectorPattern);
  return match ? `{${match[1]}}` : '';
}

/** Line filter terms (`|= "x"`, `|~ "x"`) used to highlight matches. */
export function lineFilterTerms(query: string): { text: string; regex: boolean }[] {
  const terms: { text: string; regex: boolean }[] = [];
  const pattern = /\|(=|~)\s*(?:"((?:[^"\\]|\\.)*)"|`([^`]*)`)/g;
  for (const match of query.matchAll(pattern)) {
    const raw = match[2] !== undefined ? match[2].replace(/\\(.)/g, '$1') : match[3];
    if (raw) terms.push({ text: raw, regex: match[1] === '~' });
  }
  return terms;
}

export type Segment = { text: string; match: boolean };

const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * Turns a line filter term into a JS RegExp. LogQL regexes are RE2 and often
 * start with inline flags such as `(?i)`, which JS rejects, so a leading flag
 * group is translated into RegExp flags. Invalid patterns match literally.
 */
function termPattern(term: { text: string; regex: boolean }): RegExp {
  if (!term.regex) return new RegExp(escapeRegExp(term.text), 'g');
  let source = term.text;
  let flags = 'g';
  const inline = source.match(/^\(\?([imsU]+)\)/);
  if (inline) {
    source = source.slice(inline[0].length);
    for (const flag of new Set(inline[1])) if (flag !== 'U') flags += flag;
  }
  try {
    return new RegExp(source, flags);
  } catch {
    return new RegExp(escapeRegExp(term.text), 'g');
  }
}

export function highlightSegments(line: string, terms: { text: string; regex: boolean }[]): Segment[] {
  if (!terms.length || !line) return [{ text: line, match: false }];
  // Each term keeps its own flags; overlapping matches are merged.
  const ranges: [number, number][] = [];
  for (const term of terms) {
    for (const match of line.matchAll(termPattern(term))) {
      if (match[0]) ranges.push([match.index ?? 0, (match.index ?? 0) + match[0].length]);
    }
  }
  if (!ranges.length) return [{ text: line, match: false }];
  ranges.sort((a, b) => a[0] - b[0] || b[1] - a[1]);
  const segments: Segment[] = [];
  let last = 0;
  for (const [start, end] of ranges) {
    if (end <= last) continue;
    const from = Math.max(start, last);
    if (from > last) segments.push({ text: line.slice(last, from), match: false });
    segments.push({ text: line.slice(from, end), match: true });
    last = end;
  }
  if (last < line.length) segments.push({ text: line.slice(last), match: false });
  return segments;
}

export type Level = 'error' | 'warn' | 'info' | 'debug' | 'trace' | 'unknown';

const levelAliases: Record<string, Level> = {
  emerg: 'error', alert: 'error', crit: 'error', critical: 'error', fatal: 'error', panic: 'error', error: 'error', err: 'error', eror: 'error',
  warn: 'warn', warning: 'warn', wrn: 'warn',
  info: 'info', information: 'info', inf: 'info', notice: 'info',
  debug: 'debug', dbg: 'debug', trace: 'trace',
};

/** Detects a log level from labels first, then from common line formats. */
export function detectLevel(labels: Record<string, string>, line: string): Level {
  for (const key of ['level', 'detected_level', 'severity', 'lvl', 'loglevel']) {
    const value = labels[key];
    if (value && levelAliases[value.toLowerCase()]) return levelAliases[value.toLowerCase()];
  }
  const head = line.slice(0, 512);
  const structured = head.match(/(?:"?(?:level|lvl|severity)"?\s*[:=]\s*"?)([a-zA-Z]+)/i);
  if (structured && levelAliases[structured[1].toLowerCase()]) return levelAliases[structured[1].toLowerCase()];
  const word = head.match(/\b(FATAL|PANIC|CRITICAL|ERROR|ERR|WARNING|WARN|INFO|DEBUG|TRACE)\b/);
  if (word) return levelAliases[word[1].toLowerCase()] ?? 'unknown';
  return 'unknown';
}

/** Labels that differ between streams; shared ones are hidden from rows. */
export function varyingLabels(entries: { labels: Record<string, string> }[]): string[] {
  const values = new Map<string, Set<string>>();
  let rows = 0;
  for (const entry of entries) {
    rows += 1;
    for (const [key, value] of Object.entries(entry.labels)) {
      if (!values.has(key)) values.set(key, new Set());
      values.get(key)!.add(value);
    }
  }
  if (rows === 0) return [];
  const keys: string[] = [];
  for (const [key, set] of values) {
    const present = entries.filter(entry => key in entry.labels).length;
    if (set.size > 1 || present !== rows) keys.push(key);
  }
  return keys.sort();
}

export function isMetricQuery(query: string): boolean {
  const q = query.trim();
  return /^(sum|avg|min|max|count|stddev|stdvar|topk|bottomk|sort|sort_desc|rate|count_over_time|bytes_rate|bytes_over_time|avg_over_time|sum_over_time|min_over_time|max_over_time|first_over_time|last_over_time|quantile_over_time|absent_over_time|label_replace|vector|rate_counter|stddev_over_time|stdvar_over_time|approx_topk)\s*(by|without)?\s*\(/i.test(q);
}

export function addHistory(history: string[], query: string, limit = 50): string[] {
  const value = query.trim();
  if (!value) return history;
  return [value, ...history.filter(item => item !== value)].slice(0, limit);
}

export function seriesName(labels: Record<string, string>): string {
  const entries = Object.entries(labels);
  if (!entries.length) return '{}';
  return `{${entries.map(([key, value]) => `${key}="${value}"`).join(', ')}}`;
}

export function datetimeLocalToMs(value: string): number | null {
  if (!value) return null;
  const ms = new Date(value).getTime();
  return Number.isFinite(ms) ? ms : null;
}

export function msToDatetimeLocal(ms: number): string {
  const date = new Date(ms);
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
