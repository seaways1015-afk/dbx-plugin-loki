<script setup lang="ts">
import { computed, ref } from 'vue';
import type { HistogramResult } from '../bridge';
import type { Messages } from '../i18n';

const props = defineProps<{ data: HistogramResult; startMs: number; endMs: number; utc: boolean; t: Messages }>();
const emit = defineEmits<{ zoom: [startMs: number, endMs: number] }>();

const hover = ref<number | null>(null);

const bars = computed(() => {
  const span = Math.max(1, props.endMs - props.startMs);
  const stepMs = props.data.step * 1000;
  const values = props.data.buckets.map(point => ({ t: point.t * 1000, v: Number(point.v) || 0 }));
  const max = Math.max(1, ...values.map(point => point.v));
  return values.map(point => {
    // Buckets are stamped at their end and start at the previous step boundary
    // (the window edges cut the first and last bucket short).
    const from = Math.max(props.startMs, Math.ceil(point.t / stepMs) * stepMs - stepMs);
    const to = Math.min(props.endMs, point.t);
    return {
      from,
      to,
      value: point.v,
      x: ((from - props.startMs) / span) * 100,
      w: Math.max(0.15, ((to - from) / span) * 100 - 0.12),
      h: (point.v / max) * 100,
    };
  });
});

const max = computed(() => Math.max(0, ...bars.value.map(bar => bar.value)));

function label(ms: number): string {
  const date = new Date(ms);
  const opts: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric', hour12: false };
  if (props.utc) opts.timeZone = 'UTC';
  return date.toLocaleString(undefined, opts);
}

const hovered = computed(() => (hover.value === null ? null : bars.value[hover.value]));
</script>

<template>
  <div class="histogram">
    <div class="meta">
      <span>{{ t.logVolume }} · {{ data.total.toLocaleString() }} {{ t.lines }} · max {{ max.toLocaleString() }}</span>
      <span v-if="hovered" class="tip">{{ label(hovered.from) }} – {{ label(hovered.to) }}: <b>{{ hovered.value.toLocaleString() }}</b></span>
      <span v-else class="hint">{{ t.zoomHint }}</span>
    </div>
    <div class="plot" role="img" :aria-label="`${t.logVolume}: ${data.total}`" @mouseleave="hover = null">
      <button
        v-for="(bar, index) in bars"
        :key="bar.from"
        class="bar"
        type="button"
        :style="{ left: `${bar.x}%`, width: `${bar.w}%`, height: `${Math.max(bar.value ? 2 : 0, bar.h)}%` }"
        :aria-label="`${label(bar.from)}: ${bar.value}`"
        @mouseenter="hover = index"
        @focus="hover = index"
        @click="emit('zoom', bar.from, bar.to)"
      />
    </div>
    <div class="axis">
      <span>{{ label(startMs) }}</span>
      <span>{{ label((startMs + endMs) / 2) }}</span>
      <span>{{ label(endMs) }}</span>
    </div>
  </div>
</template>

<style scoped>
.histogram { padding: 8px 14px 4px; border-bottom: 1px solid var(--border); }
.meta { display: flex; justify-content: space-between; gap: 12px; font-size: 11px; color: var(--muted); }
.tip { color: var(--fg); font-variant-numeric: tabular-nums; }
.plot { position: relative; height: 64px; margin-top: 4px; border-bottom: 1px solid var(--border); }
.bar {
  position: absolute;
  bottom: 0;
  padding: 0;
  border: 0;
  border-radius: 2px 2px 0 0;
  background: color-mix(in srgb, var(--accent) 70%, transparent);
  cursor: zoom-in;
}
.bar:hover, .bar:focus-visible { background: var(--accent); outline: none; }
.axis { display: flex; justify-content: space-between; padding-top: 2px; font-size: 10px; color: var(--muted); font-variant-numeric: tabular-nums; }
</style>
