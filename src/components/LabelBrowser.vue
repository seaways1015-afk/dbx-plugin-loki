<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import type { Messages } from '../i18n';

const props = defineProps<{
  labels: string[];
  loading: boolean;
  error: string;
  t: Messages;
  loadValues: (label: string) => Promise<{ values: string[]; truncated: boolean }>;
}>();
const emit = defineEmits<{ pick: [label: string, value: string, operator: '=' | '!=']; retry: [] }>();

const filter = ref('');
const expanded = reactive<Record<string, { loading: boolean; values: string[]; truncated: boolean; error: string; filter: string }>>({});

const visible = computed(() => {
  const q = filter.value.trim().toLowerCase();
  return props.labels.filter(label => !q || label.toLowerCase().includes(q));
});

async function toggle(label: string) {
  if (expanded[label]) {
    delete expanded[label];
    return;
  }
  expanded[label] = { loading: true, values: [], truncated: false, error: '', filter: '' };
  try {
    const result = await props.loadValues(label);
    if (expanded[label]) Object.assign(expanded[label], { loading: false, values: result.values, truncated: result.truncated });
  } catch (error) {
    if (expanded[label]) Object.assign(expanded[label], { loading: false, error: error instanceof Error ? error.message : String(error) });
  }
}

function filtered(label: string): string[] {
  const state = expanded[label];
  const q = state.filter.trim().toLowerCase();
  const values = q ? state.values.filter(value => value.toLowerCase().includes(q)) : state.values;
  return values.slice(0, 500);
}

function reset() {
  for (const key of Object.keys(expanded)) delete expanded[key];
}

defineExpose({ reset });
</script>

<template>
  <aside class="labels">
    <div class="labels-head">
      <span>{{ t.labels }}</span>
      <span class="count">{{ labels.length }}</span>
    </div>
    <input v-model="filter" class="search" type="search" :placeholder="t.filterLabels" />
    <div class="labels-body">
      <div v-if="loading" class="hint">…</div>
      <div v-else-if="error" class="hint error">
        {{ error }}
        <button class="link" type="button" @click="emit('retry')">{{ t.retry }}</button>
      </div>
      <div v-else-if="!labels.length" class="hint">{{ t.noLabels }}</div>
      <div v-for="label in visible" :key="label" class="label">
        <button class="label-name" type="button" :aria-expanded="!!expanded[label]" @click="toggle(label)">
          <span class="chevron" :class="{ open: expanded[label] }">›</span>
          {{ label }}
        </button>
        <div v-if="expanded[label]" class="values">
          <div v-if="expanded[label].loading" class="hint">{{ t.loadingValues }}</div>
          <div v-else-if="expanded[label].error" class="hint error">{{ expanded[label].error }}</div>
          <template v-else>
            <input v-if="expanded[label].values.length > 8" v-model="expanded[label].filter" class="search small" type="search" :placeholder="t.filterLabels" />
            <div v-for="value in filtered(label)" :key="value" class="value">
              <button class="value-name" type="button" :title="`${label}=&quot;${value}&quot; — ${t.include}`" @click="emit('pick', label, value, '=')">{{ value }}</button>
              <button class="exclude" type="button" :title="t.exclude" :aria-label="t.exclude" @click="emit('pick', label, value, '!=')">≠</button>
            </div>
            <div v-if="expanded[label].truncated" class="hint">{{ t.valuesTruncated }}</div>
          </template>
        </div>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.labels {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-right: 1px solid var(--border);
  background: var(--panel);
}
.labels-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px 6px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted);
}
.count { font-variant-numeric: tabular-nums; }
.search {
  margin: 0 10px 8px;
  padding: 5px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--input-bg);
  color: var(--fg);
  font: inherit;
  font-size: 12px;
}
.search.small { margin: 4px 0 4px 18px; width: calc(100% - 26px); }
.search:focus { outline: none; border-color: var(--accent); }
.labels-body { flex: 1; overflow: auto; padding: 0 4px 12px; }
.label-name, .value-name, .exclude, .link {
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
  text-align: left;
}
.label-name {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 4px 8px;
  border-radius: 4px;
  font-family: var(--mono);
  font-size: 12px;
  color: var(--label);
}
.label-name:hover, .value:hover { background: var(--hover); }
.chevron { display: inline-block; width: 10px; transition: transform 0.12s; color: var(--muted); }
.chevron.open { transform: rotate(90deg); }
.values { padding-bottom: 4px; }
.value { display: flex; align-items: center; border-radius: 4px; }
.value-name {
  flex: 1;
  min-width: 0;
  padding: 3px 8px 3px 26px;
  overflow: hidden;
  font-family: var(--mono);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.exclude { padding: 2px 8px; color: var(--muted); visibility: hidden; }
.value:hover .exclude { visibility: visible; }
.exclude:hover { color: var(--danger); }
.hint { padding: 6px 12px; font-size: 12px; color: var(--muted); }
.hint.error { color: var(--danger); word-break: break-word; }
.link { margin-left: 6px; color: var(--accent); text-decoration: underline; }
</style>
