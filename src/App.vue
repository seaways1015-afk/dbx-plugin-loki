<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { call, errorMessage, loadState, plugin, saveState, type HistogramResult, type LogEntry, type QueryForm, type QueryResult } from './bridge';
import { messages } from './i18n';
import {
  addHistory, addLabelMatcher, compareNs, datetimeLocalToMs, formatBytes, formatTimestamp, isMetricQuery,
  lineFilterTerms, LiveCursor, msToDatetimeLocal, msToNs, rangePresets,
} from './logql';
import LabelBrowser from './components/LabelBrowser.vue';
import LogList from './components/LogList.vue';
import LogQLEditor from './components/LogQLEditor.vue';
import MetricView from './components/MetricView.vue';
import VolumeHistogram from './components/VolumeHistogram.vue';

type Settings = { range: string; limit: number; direction: 'backward' | 'forward'; wrap: boolean; showLabels: boolean; utc: boolean };

const liveIntervalMs = 2000;
const liveBuffer = 5000;
// A failed poll is retried with a growing delay (4, 8, 16, 16, 16 s) before
// live tail gives up, so a short network or Loki hiccup does not stop it.
const liveMaxRetries = 5;
const liveMaxDelayMs = 16_000;

const locale = ref(window.dbxPlugin?.locale ?? navigator.language);
const t = computed(() => messages(locale.value));
const connectionId = ref('');
const bridgeError = ref('');
const preview = Boolean((window as { __LOKI_PREVIEW__?: boolean }).__LOKI_PREVIEW__);

const query = ref('');
const history = ref<string[]>([]);
const showHistory = ref(false);
const settings = ref<Settings>({ range: '1h', limit: 1000, direction: 'backward', wrap: false, showLabels: true, utc: false });
const customFrom = ref('');
const customTo = ref('');

const labels = ref<string[]>([]);
// Below 720px the label browser becomes a drawer opened from the query bar.
const labelsOpen = ref(false);
const labelsLoading = ref(false);
const labelsError = ref('');

const result = shallowRef<QueryResult | null>(null);
const entries = shallowRef<LogEntry[]>([]);
const histogram = shallowRef<HistogramResult | null>(null);
const resultWindow = ref({ startMs: 0, endMs: 0 });
// Loki returned as many lines as the limit asked for, so more may match.
const limitHit = ref(false);
const loading = ref(false);
const error = ref('');
const notice = ref('');
const ranQuery = ref('');

const live = ref(false);
const liveFailures = ref(0);
let liveTimer: ReturnType<typeof setTimeout> | undefined;
let liveCursor: LiveCursor | undefined;
let runToken = 0;

const labelBrowser = ref<InstanceType<typeof LabelBrowser>>();
const logList = ref<InstanceType<typeof LogList>>();

const terms = computed(() => lineFilterTerms(ranQuery.value));
const canAskAi = computed(() => Boolean(window.dbxPlugin?.capabilities?.ai && window.dbxPlugin?.ai));
const isMetric = computed(() => result.value !== null && result.value.resultType !== 'streams');
const statsLine = computed(() => {
  const stats = result.value?.stats;
  if (!stats) return '';
  const parts: string[] = [];
  if (!isMetric.value) {
    parts.push(`${entries.value.length.toLocaleString()} ${t.value.lines}`);
    if (stats.streams) parts.push(`${stats.streams} ${t.value.streams}`);
  } else if (result.value?.series) {
    parts.push(`${result.value.series.length} ${t.value.series}`);
  }
  if (stats.bytesProcessed) parts.push(`${formatBytes(stats.bytesProcessed)} ${t.value.processed}`);
  if (stats.execTimeSeconds) parts.push(`${(stats.execTimeSeconds * 1000).toFixed(0)} ms`);
  return parts.join(' · ');
});

function currentWindow(): { startMs: number; endMs: number } {
  if (settings.value.range === 'custom') {
    const from = datetimeLocalToMs(customFrom.value);
    const to = datetimeLocalToMs(customTo.value) ?? Date.now();
    if (from !== null && from < to) return { startMs: from, endMs: to };
  }
  const preset = rangePresets.find(item => item.id === settings.value.range) ?? rangePresets[2];
  const endMs = Date.now();
  return { startMs: endMs - preset.seconds * 1000, endMs };
}

function windowForm(range = currentWindow()): QueryForm {
  return { start: msToNs(range.startMs), end: msToNs(range.endMs) };
}

async function persist() {
  await saveState('settings', settings.value);
  await saveState('history', history.value);
  await saveState('lastQuery', query.value);
}

async function loadLabels() {
  if (!connectionId.value) return;
  labelsLoading.value = true;
  labelsError.value = '';
  try {
    const response = await call<{ labels: string[] }>(connectionId.value, 'loki/labels', windowForm());
    labels.value = response.labels;
    labelBrowser.value?.reset();
  } catch (err) {
    labelsError.value = errorMessage(err);
  } finally {
    labelsLoading.value = false;
  }
}

function loadLabelValues(label: string) {
  return call<{ values: string[]; truncated: boolean }>(connectionId.value, 'loki/label_values', { ...windowForm(), label });
}

async function editorValues(label: string): Promise<string[]> {
  return (await loadLabelValues(label)).values;
}

function metricStep(range: { startMs: number; endMs: number }): number {
  return Math.max(1, Math.ceil((range.endMs - range.startMs) / 1000 / 250));
}

async function run() {
  const q = query.value.trim();
  if (!q || !connectionId.value) return;
  stopLive();
  const token = ++runToken;
  const range = currentWindow();
  loading.value = true;
  error.value = '';
  notice.value = '';
  showHistory.value = false;
  history.value = addHistory(history.value, q);
  void persist();
  try {
    const metric = isMetricQuery(q);
    const form: QueryForm = { ...windowForm(range), query: q, limit: settings.value.limit, direction: settings.value.direction };
    if (metric) form.step = metricStep(range);
    const [queryResult, volume] = await Promise.all([
      call<QueryResult>(connectionId.value, 'loki/query_range', form),
      metric ? Promise.resolve(null) : call<HistogramResult>(connectionId.value, 'loki/histogram', { ...windowForm(range), query: q }).catch(() => null),
    ]);
    if (token !== runToken) return;
    result.value = queryResult;
    entries.value = queryResult.entries ?? [];
    histogram.value = queryResult.resultType === 'streams' ? volume : null;
    limitHit.value = queryResult.resultType === 'streams' && queryResult.stats.entries >= settings.value.limit;
    resultWindow.value = range;
    ranQuery.value = q;
    logList.value?.resetPaging();
  } catch (err) {
    if (token !== runToken) return;
    // Do not leave the previous query's lines and histogram under the error.
    result.value = null;
    entries.value = [];
    histogram.value = null;
    limitHit.value = false;
    ranQuery.value = '';
    error.value = errorMessage(err);
  } finally {
    if (token === runToken) loading.value = false;
  }
}

function newestTimestamp(list: LogEntry[]): string {
  let newest = '';
  for (const entry of list) if (!newest || compareNs(entry.ts, newest) > 0) newest = entry.ts;
  return newest;
}

function startLive() {
  const q = query.value.trim();
  if (!q || isMetricQuery(q) || !connectionId.value) return;
  if (ranQuery.value !== q || isMetric.value) {
    result.value = { resultType: 'streams', entries: [], truncated: false, stats: { streams: 0, entries: 0, bytesProcessed: 0, execTimeSeconds: 0 } };
    entries.value = [];
    histogram.value = null;
    ranQuery.value = q;
    history.value = addHistory(history.value, q);
    void persist();
  }
  error.value = '';
  live.value = true;
  liveFailures.value = 0;
  liveCursor = new LiveCursor(newestTimestamp(entries.value) || msToNs(Date.now()), entries.value);
  void poll();
}

async function poll() {
  if (!live.value || !liveCursor) return;
  const cursor = liveCursor;
  try {
    const response = await call<QueryResult>(connectionId.value, 'loki/tail', { query: ranQuery.value, start: cursor.start }, 30_000);
    if (!live.value || cursor !== liveCursor) return;
    const fresh = cursor.accept(response.entries ?? []);
    if (fresh.length) {
      // Late lines can be older than lines already shown, so re-sort instead of prepending.
      const backward = settings.value.direction === 'backward';
      const merged = entries.value.concat(fresh).sort((a, b) => (backward ? compareNs(b.ts, a.ts) : compareNs(a.ts, b.ts)));
      entries.value = backward ? merged.slice(0, liveBuffer) : merged.slice(-liveBuffer);
    }
    liveFailures.value = 0;
  } catch (err) {
    if (!live.value || cursor !== liveCursor) return;
    liveFailures.value += 1;
    if (liveFailures.value > liveMaxRetries) {
      error.value = errorMessage(err);
      stopLive();
      return;
    }
    liveTimer = setTimeout(poll, Math.min(liveIntervalMs * 2 ** liveFailures.value, liveMaxDelayMs));
    return;
  }
  liveTimer = setTimeout(poll, liveIntervalMs);
}

function stopLive() {
  live.value = false;
  if (liveTimer) clearTimeout(liveTimer);
  liveTimer = undefined;
}

function pickLabel(label: string, value: string, operator: '=' | '!=') {
  query.value = addLabelMatcher(query.value, label, value, operator);
  labelsOpen.value = false;
}

function applyFilterAndRun(label: string, value: string, operator: '=' | '!=') {
  pickLabel(label, value, operator);
  void run();
}

function zoom(startMs: number, endMs: number) {
  settings.value.range = 'custom';
  // The inputs hold whole minutes: round the start down and the end up.
  customFrom.value = msToDatetimeLocal(startMs);
  customTo.value = msToDatetimeLocal(Math.ceil(endMs / 60_000) * 60_000);
  void run();
}

function changeRange() {
  if (settings.value.range === 'custom' && !customFrom.value) {
    const { startMs, endMs } = currentWindowFromPreset();
    customFrom.value = msToDatetimeLocal(startMs);
    customTo.value = msToDatetimeLocal(endMs);
  }
  void persist();
  if (settings.value.range !== 'custom') {
    void loadLabels();
    if (ranQuery.value) void run();
  }
}

function currentWindowFromPreset() {
  const endMs = Date.now();
  return { startMs: endMs - 3600_000, endMs };
}

async function copy(text: string) {
  try {
    const api = plugin();
    if (api.clipboard) await api.clipboard.writeText(text);
    else await navigator.clipboard.writeText(text);
    flash(t.value.copied);
  } catch {
    flash(t.value.copyFailed);
  }
}

let noticeTimer: ReturnType<typeof setTimeout> | undefined;
function flash(message: string) {
  notice.value = message;
  if (noticeTimer) clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => (notice.value = ''), 1800);
}

async function askAi() {
  const api = plugin();
  if (!api.ai) return;
  const sample = entries.value.slice(0, 150).map(entry => `${formatTimestamp(entry.ts, true)} ${JSON.stringify(entry.labels)} ${entry.line.slice(0, 1000)}`);
  await api.ai.openConversation({
    title: `Loki: ${ranQuery.value.slice(0, 60)}`,
    prompt: t.value.askAiPrompt,
    context: {
      connectionId: connectionId.value,
      query: ranQuery.value,
      range: { from: new Date(resultWindow.value.startMs).toISOString(), to: new Date(resultWindow.value.endMs).toISOString() },
      totalLines: entries.value.length,
      lines: sample,
    },
    mode: 'ask',
  });
}

function useHistory(item: string) {
  query.value = item;
  showHistory.value = false;
  void run();
}

function applyTheme() {
  const theme = window.dbxPlugin?.theme;
  const root = document.documentElement;
  root.dataset.appearance = theme?.appearance ?? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
  if (theme?.editor?.fontSize) root.style.setProperty('--editor-size', `${theme.editor.fontSize}px`);
  if (theme?.editor?.fontFamily) root.style.setProperty('--mono', theme.editor.fontFamily);
}

function onEnv() {
  locale.value = window.dbxPlugin?.locale ?? locale.value;
  applyTheme();
}

let offContext: (() => void) | undefined;

onMounted(async () => {
  applyTheme();
  window.addEventListener('dbx-plugin-env', onEnv);
  if (!window.dbxPlugin) {
    bridgeError.value = t.value.notConnectedBody;
    return;
  }
  try {
    await window.dbxPlugin.ready;
  } catch (err) {
    bridgeError.value = errorMessage(err);
    return;
  }
  onEnv();
  const [savedSettings, savedHistory, savedQuery] = await Promise.all([
    loadState<Partial<Settings>>('settings'),
    loadState<string[]>('history'),
    loadState<string>('lastQuery'),
  ]);
  if (savedSettings) settings.value = { ...settings.value, ...savedSettings, range: savedSettings.range === 'custom' ? '1h' : savedSettings.range ?? '1h' };
  if (savedHistory) history.value = savedHistory;
  if (savedQuery) query.value = savedQuery;
  connectionId.value = window.dbxPlugin.context?.connectionId ?? '';
  offContext = window.dbxPlugin.onContext(context => {
    const next = context.connectionId ?? '';
    if (next && next !== connectionId.value) {
      connectionId.value = next;
      stopLive();
      void loadLabels();
    }
  });
  if (connectionId.value) {
    await loadLabels();
    if (query.value) void run();
  }
});

onBeforeUnmount(() => {
  stopLive();
  offContext?.();
  window.removeEventListener('dbx-plugin-env', onEnv);
});
</script>

<template>
  <div class="app">
    <div v-if="preview" class="preview-banner">{{ t.preview }}</div>
    <div v-if="!connectionId" class="empty-state">
      <h2>{{ t.notConnectedTitle }}</h2>
      <p>{{ bridgeError || t.notConnectedBody }}</p>
    </div>
    <div v-else class="layout" :class="{ 'labels-open': labelsOpen }">
      <div v-if="labelsOpen" class="labels-backdrop" @click="labelsOpen = false" />
      <LabelBrowser
        ref="labelBrowser"
        class="label-pane"
        :labels="labels"
        :loading="labelsLoading"
        :error="labelsError"
        :t="t"
        :load-values="loadLabelValues"
        @pick="pickLabel"
        @retry="loadLabels"
      />
      <main class="main">
        <div class="query-bar">
          <LogQLEditor v-model="query" :placeholder="'{app=&quot;api&quot;} |= &quot;error&quot;'" :labels="labels" :load-values="editorValues" @run="run" />
          <div class="query-actions">
            <button class="btn labels-toggle" type="button" :aria-expanded="labelsOpen" @click="labelsOpen = !labelsOpen">{{ t.labels }}</button>
            <button class="btn primary" type="button" :disabled="loading || !query.trim()" :title="t.runHint" @click="run">
              {{ loading ? t.running : t.run }}
            </button>
            <button
              class="btn"
              :class="{ live }"
              type="button"
              :disabled="!query.trim() || isMetricQuery(query)"
              :aria-pressed="live"
              @click="live ? stopLive() : startLive()"
            >
              <span class="dot" />{{ live ? t.stop : t.live }}
            </button>
            <div class="history-wrap">
              <button class="btn" type="button" :aria-expanded="showHistory" @click="showHistory = !showHistory">{{ t.history }}</button>
              <div v-if="showHistory" class="history">
                <div v-if="!history.length" class="hint">{{ t.noHistory }}</div>
                <button v-for="item in history" :key="item" type="button" class="history-item" @click="useHistory(item)">{{ item }}</button>
              </div>
            </div>
          </div>
        </div>
        <div class="toolbar">
          <label class="field">
            <span>{{ t.range }}</span>
            <select v-model="settings.range" @change="changeRange">
              <option v-for="preset in rangePresets" :key="preset.id" :value="preset.id">{{ t.presets[preset.id] }}</option>
              <option value="custom">{{ t.custom }}</option>
            </select>
          </label>
          <template v-if="settings.range === 'custom'">
            <label class="field"><span>{{ t.from }}</span><input v-model="customFrom" type="datetime-local" /></label>
            <label class="field"><span>{{ t.to }}</span><input v-model="customTo" type="datetime-local" /></label>
            <button class="btn small" type="button" @click="loadLabels(); run()">{{ t.apply }}</button>
          </template>
          <label class="field">
            <span>{{ t.limit }}</span>
            <select v-model.number="settings.limit" @change="persist">
              <option v-for="n in [100, 500, 1000, 2000, 5000]" :key="n" :value="n">{{ n }}</option>
            </select>
          </label>
          <select v-model="settings.direction" class="plain" @change="persist">
            <option value="backward">{{ t.newestFirst }}</option>
            <option value="forward">{{ t.oldestFirst }}</option>
          </select>
          <span class="spacer" />
          <label class="check"><input v-model="settings.wrap" type="checkbox" @change="persist" />{{ t.wrap }}</label>
          <label class="check"><input v-model="settings.showLabels" type="checkbox" @change="persist" />{{ t.showLabels }}</label>
          <label class="check"><input v-model="settings.utc" type="checkbox" @change="persist" />{{ t.utc }}</label>
        </div>

        <div v-if="error" class="error-box" role="alert"><b>{{ t.error }}:</b> {{ error }}</div>

        <VolumeHistogram
          v-if="histogram && histogram.buckets.length && !live"
          :data="histogram"
          :start-ms="resultWindow.startMs"
          :end-ms="resultWindow.endMs"
          :utc="settings.utc"
          :t="t"
          @zoom="zoom"
        />

        <div v-if="result" class="stats">
          <span v-if="live" class="live-badge"><span class="dot" />{{ t.liveOn }}</span>
          <span v-if="live && liveFailures" class="warn">{{ t.liveRetrying }} ({{ liveFailures }}/{{ liveMaxRetries }})</span>
          <span>{{ statsLine }}</span>
          <span v-if="result.truncated" class="warn">· {{ t.truncated }}</span>
          <span v-else-if="limitHit && !live" class="warn">· {{ t.limitReached }}</span>
          <span class="spacer" />
          <span v-if="notice" class="notice">{{ notice }}</span>
          <button v-if="canAskAi && !isMetric && entries.length" class="btn small" type="button" @click="askAi">{{ t.askAi }}</button>
        </div>

        <div class="results">
          <div v-if="!result && !loading && !error" class="empty-state inline">
            <h3>{{ t.emptyTitle }}</h3>
            <p>{{ t.emptyBody }}</p>
          </div>
          <MetricView v-else-if="result && isMetric" :result="result" :utc="settings.utc" :t="t" />
          <div v-else-if="result && !entries.length && !live" class="hint pad">{{ t.noResults }}</div>
          <LogList
            v-else-if="result"
            ref="logList"
            :entries="entries"
            :terms="terms"
            :wrap="settings.wrap"
            :show-labels="settings.showLabels"
            :utc="settings.utc"
            :t="t"
            @filter="applyFilterAndRun"
            @copy="copy"
          />
        </div>
      </main>
    </div>
  </div>
</template>

<style>
:root {
  --bg: var(--color-background, #ffffff);
  --fg: var(--color-foreground, #18181b);
  --panel: var(--color-card, #fafafa);
  --muted: var(--color-muted-foreground, #71717a);
  --border: var(--color-border, #e4e4e7);
  --row-border: color-mix(in srgb, var(--border) 55%, transparent);
  --hover: color-mix(in srgb, var(--fg) 5%, transparent);
  --input-bg: var(--color-background, #ffffff);
  --accent: var(--color-primary, #2563eb);
  --accent-fg: var(--color-primary-foreground, #ffffff);
  --danger: var(--color-destructive, #dc2626);
  --radius: var(--radius-md, 6px);
  --sans: var(--font-sans, Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif);
  --mono: var(--font-mono, ui-monospace, 'JetBrains Mono', SFMono-Regular, Menlo, Consolas, monospace);
  --editor-size: 13px;
  --log-size: 12px;
  --label: #7c3aed;
  --chip-bg: color-mix(in srgb, #7c3aed 9%, transparent);
  --mark-bg: color-mix(in srgb, #f59e0b 38%, transparent);
  --details-bg: color-mix(in srgb, var(--fg) 3%, transparent);
  --level-error: #e5484d;
  --level-warn: #f5a524;
  --level-info: #3fb27f;
  --level-debug: #6e9fff;
  --level-unknown: color-mix(in srgb, var(--fg) 15%, transparent);
  --lq-string: #0a7d3e;
  --lq-number: #b45309;
  --lq-keyword: #1d4ed8;
  --lq-operator: #be185d;
  --lq-label: #7c3aed;
  --lq-comment: #71717a;
  color-scheme: light;
}
:root[data-appearance='dark'] {
  --bg: var(--color-background, #18181b);
  --fg: var(--color-foreground, #e4e4e7);
  --panel: var(--color-card, #1f1f23);
  --muted: var(--color-muted-foreground, #a1a1aa);
  --border: var(--color-border, #2e2e33);
  --input-bg: var(--color-background, #131316);
  --label: #c4a5ff;
  --chip-bg: color-mix(in srgb, #c4a5ff 14%, transparent);
  --mark-bg: color-mix(in srgb, #f59e0b 35%, transparent);
  --lq-string: #7ee2a8;
  --lq-number: #fbbf72;
  --lq-keyword: #8cb4ff;
  --lq-operator: #f78fc4;
  --lq-label: #c4a5ff;
  --lq-comment: #8b8b94;
  color-scheme: dark;
}
* { box-sizing: border-box; }
html, body, #app { height: 100%; margin: 0; }
body { background: var(--bg); color: var(--fg); font-family: var(--sans); font-size: 13px; font-variant-ligatures: none; }
</style>

<style scoped>
.app { display: flex; flex-direction: column; height: 100%; }
.preview-banner { padding: 4px 12px; background: #f59e0b; color: #1c1917; font-size: 12px; font-weight: 600; }
.layout { position: relative; display: grid; flex: 1; grid-template-columns: minmax(180px, 240px) 1fr; min-height: 0; }
.btn.labels-toggle, .labels-backdrop { display: none; }
.main { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
.query-bar { display: flex; gap: 8px; align-items: flex-start; padding: 10px 12px 6px; }
.query-actions { display: flex; gap: 6px; }
.toolbar { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; padding: 2px 12px 8px; border-bottom: 1px solid var(--border); font-size: 12px; }
.field { display: inline-flex; gap: 6px; align-items: center; color: var(--muted); }
select, input[type='datetime-local'] {
  padding: 3px 6px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--input-bg);
  color: var(--fg);
  font: inherit;
}
select.plain { color: var(--fg); }
.check { display: inline-flex; gap: 4px; align-items: center; color: var(--muted); cursor: pointer; }
.spacer { flex: 1; }
.btn {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--panel);
  color: var(--fg);
  font: inherit;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
}
.btn:hover:not(:disabled) { border-color: var(--accent); }
.btn:disabled { opacity: 0.5; cursor: default; }
.btn.primary { border-color: var(--accent); background: var(--accent); color: var(--accent-fg); }
.btn.small { height: 24px; padding: 0 8px; font-size: 12px; }
.btn .dot, .live-badge .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
.btn.live { border-color: var(--level-info); color: var(--level-info); }
.btn.live .dot, .live-badge .dot { background: var(--level-info); animation: pulse 1.2s ease-in-out infinite; }
@keyframes pulse { 50% { opacity: 0.3; } }
.history-wrap { position: relative; }
.history {
  position: absolute;
  z-index: 10;
  top: 36px;
  right: 0;
  width: min(520px, 70vw);
  max-height: 320px;
  padding: 4px;
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--panel);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.18);
}
.history-item {
  display: block;
  width: 100%;
  padding: 6px 8px;
  overflow: hidden;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--fg);
  font-family: var(--mono);
  font-size: 12px;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}
.history-item:hover { background: var(--hover); }
.error-box {
  margin: 8px 12px 0;
  padding: 8px 10px;
  border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
  border-radius: var(--radius);
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  color: var(--danger);
  font-family: var(--mono);
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-word;
}
.stats { display: flex; gap: 6px; align-items: center; min-height: 32px; padding: 4px 12px; border-bottom: 1px solid var(--border); color: var(--muted); font-size: 12px; }
.stats .warn { color: var(--level-warn); }
.live-badge { display: inline-flex; gap: 6px; align-items: center; margin-right: 6px; color: var(--level-info); font-weight: 600; }
.notice { color: var(--level-info); }
.results { flex: 1; min-height: 0; overflow: auto; }
.empty-state { margin: auto; padding: 48px 24px; max-width: 520px; text-align: center; }
.empty-state.inline { padding-top: 64px; }
.empty-state h2, .empty-state h3 { margin: 0 0 8px; }
.empty-state p { margin: 0; color: var(--muted); line-height: 1.6; }
.hint { color: var(--muted); font-size: 12px; padding: 6px 8px; }
.hint.pad { padding: 24px; text-align: center; }
@media (max-width: 720px) {
  .layout { grid-template-columns: 1fr; }
  .label-pane {
    display: none;
    position: absolute;
    z-index: 20;
    top: 0;
    bottom: 0;
    left: 0;
    width: min(280px, 85vw);
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.25);
  }
  .labels-open .label-pane { display: flex; }
  .labels-open .labels-backdrop { display: block; position: absolute; z-index: 19; inset: 0; background: rgba(0, 0, 0, 0.2); }
  .btn.labels-toggle { display: inline-flex; }
  .query-bar { flex-direction: column; align-items: stretch; }
}
</style>
