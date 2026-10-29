<script setup lang="ts">
import { computed, ref } from 'vue';
import type { LogEntry } from '../bridge';
import type { Messages } from '../i18n';
import { detectLevel, entryKey, formatTimestamp, highlightSegments, varyingLabels } from '../logql';

const props = defineProps<{
  entries: LogEntry[];
  terms: { text: string; regex: boolean }[];
  wrap: boolean;
  showLabels: boolean;
  utc: boolean;
  t: Messages;
}>();
const emit = defineEmits<{ filter: [label: string, value: string, operator: '=' | '!=']; copy: [text: string] }>();

// Rendering thousands of rows is fine with content-visibility, but cap the
// DOM anyway and let the user page in more.
const pageSize = 1000;
const shown = ref(pageSize);
const expanded = ref<Set<string>>(new Set());

// Keys must not depend on the row position: live tail inserts lines above
// existing ones, and an expanded row has to stay expanded.
const varying = computed(() => varyingLabels(props.entries));
const rows = computed(() => {
  const seen = new Map<string, number>();
  return props.entries.slice(0, shown.value).map(entry => {
    const base = entryKey(entry);
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    return { entry, key: n ? `${base}#${n}` : base, ...describe(entry) };
  });
});

function describe(entry: LogEntry) {
  return {
    level: detectLevel(entry.labels, entry.line),
    time: formatTimestamp(entry.ts, props.utc),
    chips: varying.value.filter(key => key in entry.labels).map(key => [key, entry.labels[key]] as const),
    segments: highlightSegments(entry.line, props.terms),
  };
}

function toggle(key: string) {
  const next = new Set(expanded.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  expanded.value = next;
}

function resetPaging() {
  shown.value = pageSize;
  expanded.value = new Set();
}

defineExpose({ resetPaging });
</script>

<template>
  <div class="log-list" :class="{ wrap }">
    <div v-for="row in rows" :key="row.key" class="row" :class="[`level-${row.level}`, { open: expanded.has(row.key) }]">
      <div class="summary" role="button" tabindex="0" @click="toggle(row.key)" @keydown.enter.prevent="toggle(row.key)">
        <span class="bar" />
        <span class="time">{{ row.time }}</span>
        <span v-if="showLabels && row.chips.length" class="chips">
          <span v-for="[key, value] in row.chips" :key="key" class="chip" :title="`${key}=${value}`">{{ value }}</span>
        </span>
        <span class="line"><template v-for="(segment, index) in row.segments" :key="index"><mark v-if="segment.match">{{ segment.text }}</mark><template v-else>{{ segment.text }}</template></template><span v-if="row.entry.truncated" class="trunc">…</span></span>
      </div>
      <div v-if="expanded.has(row.key)" class="details">
        <div class="details-actions">
          <button type="button" class="btn" @click="emit('copy', row.entry.line)">{{ t.copy }}</button>
        </div>
        <table>
          <tbody>
            <tr v-for="(value, key) in row.entry.labels" :key="key">
              <td class="actions">
                <button type="button" class="icon" :title="t.include" :aria-label="t.include" @click="emit('filter', String(key), value, '=')">+</button>
                <button type="button" class="icon" :title="t.exclude" :aria-label="t.exclude" @click="emit('filter', String(key), value, '!=')">−</button>
              </td>
              <td class="key">{{ key }}</td>
              <td class="val">{{ value }}</td>
            </tr>
          </tbody>
        </table>
        <pre class="full">{{ row.entry.line }}</pre>
      </div>
    </div>
    <button v-if="entries.length > shown" type="button" class="more" @click="shown += pageSize">
      +{{ Math.min(pageSize, entries.length - shown) }} / {{ entries.length - shown }}
    </button>
  </div>
</template>

<style scoped>
.log-list { font-family: var(--mono); font-size: var(--log-size); line-height: 1.5; }
.row { border-bottom: 1px solid var(--row-border); content-visibility: auto; contain-intrinsic-size: auto 22px; }
.summary { display: flex; align-items: flex-start; gap: 10px; padding: 1px 12px 1px 0; cursor: pointer; }
.summary:hover { background: var(--hover); }
.summary:focus-visible { outline: 1px solid var(--accent); outline-offset: -1px; }
.bar { flex: none; align-self: stretch; width: 3px; background: var(--level-unknown); }
.level-error .bar { background: var(--level-error); }
.level-warn .bar { background: var(--level-warn); }
.level-info .bar { background: var(--level-info); }
.level-debug .bar, .level-trace .bar { background: var(--level-debug); }
.time { flex: none; color: var(--muted); font-variant-numeric: tabular-nums; white-space: nowrap; }
.chips { display: flex; flex: none; gap: 4px; max-width: 30%; overflow: hidden; }
.chip {
  max-width: 160px;
  padding: 0 6px;
  overflow: hidden;
  border-radius: 3px;
  background: var(--chip-bg);
  color: var(--label);
  font-size: 0.92em;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.line { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: pre; }
.wrap .line { white-space: pre-wrap; word-break: break-all; }
mark { border-radius: 2px; background: var(--mark-bg); color: inherit; }
.trunc { color: var(--muted); }
.details { padding: 6px 12px 10px 16px; background: var(--details-bg); }
.details-actions { margin-bottom: 6px; }
table { border-collapse: collapse; font-size: 0.95em; }
td { padding: 1px 8px 1px 0; vertical-align: top; }
.key { color: var(--label); white-space: nowrap; }
.val { word-break: break-all; }
.actions { white-space: nowrap; }
.icon, .btn, .more {
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--panel);
  color: var(--fg);
  font: inherit;
  font-family: var(--sans);
  cursor: pointer;
}
.icon { width: 20px; height: 18px; padding: 0; line-height: 1; }
.btn { padding: 2px 8px; font-size: 12px; }
.icon:hover, .btn:hover, .more:hover { border-color: var(--accent); }
.full { margin: 8px 0 0; max-height: 360px; padding: 8px; overflow: auto; border-radius: 4px; background: var(--input-bg); white-space: pre-wrap; word-break: break-all; }
.more { display: block; margin: 10px auto; padding: 4px 14px; font-size: 12px; }
</style>
