<script setup lang="ts">
import { computed } from 'vue';
import type { QueryResult } from '../bridge';
import type { Messages } from '../i18n';
import { seriesName } from '../logql';

const props = defineProps<{ result: QueryResult; t: Messages }>();

const palette = ['#4f8cf7', '#f2a33a', '#3fb27f', '#e5534b', '#a371f7', '#2fb6c4', '#d4a72c', '#db61a2'];

const chart = computed(() => {
  const series = props.result.series ?? [];
  const points = series.flatMap(serie => serie.points);
  if (points.length < 2) return null;
  const xs = points.map(point => point.t);
  const ys = points.map(point => Number(point.v)).filter(Number.isFinite);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const maxY = Math.max(0, ...ys) || 1;
  const width = 1000;
  const height = 200;
  const paths = series.slice(0, palette.length).map((serie, index) => ({
    color: palette[index],
    d: serie.points
      .map((point, i) => {
        const x = ((point.t - minX) / Math.max(1, maxX - minX)) * width;
        const y = height - (Number(point.v) / maxY) * height;
        return `${i ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(''),
  }));
  return { width, height, paths, maxY };
});

const rows = computed(() =>
  (props.result.series ?? []).map((serie, index) => ({
    name: seriesName(serie.labels),
    value: serie.points.at(-1)?.v ?? '',
    color: index < palette.length ? palette[index] : 'transparent',
  })),
);
</script>

<template>
  <div class="metric">
    <div v-if="result.scalar" class="scalar">{{ result.scalar.v }}</div>
    <template v-else>
      <div v-if="chart" class="chart">
        <span class="max">{{ chart.maxY.toLocaleString() }}</span>
        <svg :viewBox="`0 0 ${chart.width} ${chart.height}`" preserveAspectRatio="none" role="img">
          <path v-for="(path, index) in chart.paths" :key="index" :d="path.d" :stroke="path.color" fill="none" stroke-width="1.6" vector-effect="non-scaling-stroke" />
        </svg>
      </div>
      <table>
        <thead>
          <tr><th>{{ rows.length }} {{ t.series }}</th><th class="num">{{ t.value }}</th></tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.name">
            <td><span class="swatch" :style="{ background: row.color }" />{{ row.name }}</td>
            <td class="num">{{ row.value }}</td>
          </tr>
        </tbody>
      </table>
    </template>
  </div>
</template>

<style scoped>
.metric { padding: 12px 14px; }
.scalar { font-size: 28px; font-weight: 600; font-variant-numeric: tabular-nums; }
.chart { position: relative; height: 200px; margin-bottom: 12px; border-bottom: 1px solid var(--border); }
.chart svg { width: 100%; height: 100%; }
.max { position: absolute; top: 0; left: 0; font-size: 10px; color: var(--muted); }
table { width: 100%; border-collapse: collapse; font-family: var(--mono); font-size: 12px; }
th, td { padding: 4px 8px; border-bottom: 1px solid var(--row-border); text-align: left; word-break: break-all; }
th { color: var(--muted); font-family: var(--sans); font-weight: 600; }
.num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
.swatch { display: inline-block; width: 8px; height: 8px; margin-right: 8px; border-radius: 2px; }
</style>
