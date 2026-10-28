<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { EditorState, Prec } from '@codemirror/state';
import { EditorView, keymap, placeholder as placeholderExt } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { HighlightStyle, StreamLanguage, syntaxHighlighting } from '@codemirror/language';
import { autocompletion, closeBrackets, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete';
import { tags } from '@lezer/highlight';

const props = defineProps<{
  modelValue: string;
  placeholder: string;
  labels: string[];
  loadValues: (label: string) => Promise<string[]>;
}>();
const emit = defineEmits<{ 'update:modelValue': [value: string]; run: [] }>();

const host = ref<HTMLDivElement>();
let view: EditorView | undefined;

const keywords = new Set(['by', 'without', 'on', 'ignoring', 'group_left', 'group_right', 'offset', 'bool', 'and', 'or', 'unless']);
const functions = [
  'rate', 'count_over_time', 'bytes_rate', 'bytes_over_time', 'avg_over_time', 'sum_over_time', 'min_over_time', 'max_over_time',
  'stddev_over_time', 'stdvar_over_time', 'quantile_over_time', 'first_over_time', 'last_over_time', 'absent_over_time',
  'sum', 'avg', 'min', 'max', 'count', 'stddev', 'stdvar', 'topk', 'bottomk', 'sort', 'sort_desc', 'label_replace', 'vector',
];
const parsers = ['json', 'logfmt', 'regexp', 'pattern', 'unpack', 'line_format', 'label_format', 'unwrap', 'decolorize', 'drop', 'keep'];
const functionSet = new Set([...functions, ...parsers]);

// A small stream tokenizer is enough for highlighting; Loki validates syntax.
const logql = StreamLanguage.define<{ inSelector: boolean }>({
  startState: () => ({ inSelector: false }),
  token(stream, state) {
    if (stream.eatSpace()) return null;
    if (stream.match('#')) {
      stream.skipToEnd();
      return 'comment';
    }
    if (stream.match(/^"(?:[^"\\]|\\.)*"?/) || stream.match(/^`[^`]*`?/)) return 'string';
    if (stream.match(/^\[[0-9]+(?:ms|s|m|h|d|w|y)\]/)) return 'number';
    if (stream.match(/^[0-9]+(?:\.[0-9]+)?(?:ms|s|m|h|d|w|y)?/)) return 'number';
    if (stream.match(/^(\|=|\|~|!=|!~|=~|\|>|!>|==|>=|<=|\||=|>|<)/)) return 'operator';
    const ch = stream.peek();
    if (ch === '{') {
      state.inSelector = true;
      stream.next();
      return 'brace';
    }
    if (ch === '}') {
      state.inSelector = false;
      stream.next();
      return 'brace';
    }
    const word = stream.match(/^[a-zA-Z_][a-zA-Z0-9_]*/) as RegExpMatchArray | null;
    if (word) {
      const value = word[0];
      if (state.inSelector) return 'labelName';
      if (functionSet.has(value)) return 'keyword';
      if (keywords.has(value)) return 'modifier';
      return 'variableName';
    }
    stream.next();
    return null;
  },
});

const highlight = HighlightStyle.define([
  { tag: tags.string, color: 'var(--lq-string)' },
  { tag: tags.number, color: 'var(--lq-number)' },
  { tag: tags.keyword, color: 'var(--lq-keyword)', fontWeight: '600' },
  { tag: tags.modifier, color: 'var(--lq-keyword)' },
  { tag: tags.operator, color: 'var(--lq-operator)' },
  { tag: tags.labelName, color: 'var(--lq-label)' },
  { tag: tags.comment, color: 'var(--lq-comment)', fontStyle: 'italic' },
  { tag: tags.brace, color: 'var(--lq-operator)' },
]);

const valueCache = new Map<string, Promise<string[]>>();

function cachedValues(label: string): Promise<string[]> {
  if (!valueCache.has(label)) {
    const promise = props.loadValues(label).catch(() => {
      valueCache.delete(label);
      return [];
    });
    valueCache.set(label, promise);
  }
  return valueCache.get(label)!;
}

watch(() => props.labels, () => valueCache.clear());

async function complete(context: CompletionContext): Promise<CompletionResult | null> {
  const before = context.state.sliceDoc(0, context.pos);
  const open = before.lastIndexOf('{');
  const close = before.lastIndexOf('}');
  if (open > close) {
    // Inside a stream selector.
    const valueMatch = before.slice(open).match(/([a-zA-Z_][a-zA-Z0-9_]*)\s*(=~|!~|!=|=)\s*"([^"]*)$/);
    if (valueMatch) {
      const values = await cachedValues(valueMatch[1]);
      return {
        from: context.pos - valueMatch[3].length,
        options: values.map(value => ({ label: value, type: 'text', apply: value.replace(/\\/g, '\\\\').replace(/"/g, '\\"') })),
        validFor: /^[^"]*$/,
      };
    }
    const nameMatch = context.matchBefore(/[a-zA-Z_][a-zA-Z0-9_]*/);
    if (!nameMatch && !context.explicit && !/[{,]\s*$/.test(before)) return null;
    return {
      from: nameMatch ? nameMatch.from : context.pos,
      options: props.labels.map(label => ({ label, type: 'property', apply: `${label}="` })),
      validFor: /^[a-zA-Z0-9_]*$/,
    };
  }
  const word = context.matchBefore(/[a-zA-Z_][a-zA-Z0-9_]*/);
  if (!word && !context.explicit) return null;
  const afterPipe = /\|\s*[a-zA-Z_]*$/.test(before);
  const options = afterPipe
    ? parsers.map(name => ({ label: name, type: 'function' }))
    : functions.map(name => ({ label: name, type: 'function', apply: `${name}(` }));
  return { from: word ? word.from : context.pos, options, validFor: /^[a-zA-Z0-9_]*$/ };
}

onMounted(() => {
  const runKeys = Prec.highest(keymap.of([
    { key: 'Shift-Enter', run: () => { emit('run'); return true; } },
    { key: 'Mod-Enter', run: () => { emit('run'); return true; } },
  ]));
  view = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        runKeys,
        history(),
        closeBrackets(),
        keymap.of([...defaultKeymap, ...historyKeymap]),
        logql,
        syntaxHighlighting(highlight),
        autocompletion({ override: [complete], activateOnTyping: true }),
        placeholderExt(props.placeholder),
        EditorView.lineWrapping,
        EditorView.updateListener.of(update => {
          if (update.docChanged) emit('update:modelValue', update.state.doc.toString());
        }),
      ],
    }),
  });
});

watch(() => props.modelValue, value => {
  if (view && value !== view.state.doc.toString()) {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
  }
});

onBeforeUnmount(() => view?.destroy());

defineExpose({ focus: () => view?.focus() });
</script>

<template>
  <div ref="host" class="logql-editor" />
</template>

<style scoped>
.logql-editor {
  flex: 1;
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--input-bg);
  overflow: hidden;
}
.logql-editor:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 25%, transparent);
}
.logql-editor :deep(.cm-editor) {
  max-height: 160px;
  font-family: var(--mono);
  font-size: var(--editor-size);
  background: transparent;
  color: var(--fg);
}
.logql-editor :deep(.cm-editor.cm-focused) { outline: none; }
.logql-editor :deep(.cm-scroller) { font-family: var(--mono); }
.logql-editor :deep(.cm-content) { padding: 7px 4px; caret-color: var(--fg); }
.logql-editor :deep(.cm-placeholder) { color: var(--muted); }
.logql-editor :deep(.cm-tooltip) {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel);
  color: var(--fg);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
}
.logql-editor :deep(.cm-tooltip-autocomplete ul li[aria-selected]) { background: var(--accent); color: var(--accent-fg); }
.logql-editor :deep(.cm-tooltip-autocomplete ul) { font-family: var(--mono); max-height: 240px; }
</style>
