import { describe, expect, it } from 'vitest';
import {
  addHistory, addLabelMatcher, compareNs, detectLevel, formatTimestamp, highlightSegments, isMetricQuery,
  lineFilterTerms, msToNs, nextNs, nsToMs, selectorOf, splitMatchers, varyingLabels,
} from './logql';

describe('addLabelMatcher', () => {
  it('creates a selector for an empty query', () => {
    expect(addLabelMatcher('', 'app', 'api')).toBe('{app="api"}');
  });
  it('appends to an existing selector and keeps the pipeline', () => {
    expect(addLabelMatcher('{namespace="prod"} |= "error"', 'app', 'api')).toBe('{namespace="prod", app="api"} |= "error"');
  });
  it('replaces an existing matcher for the same label', () => {
    expect(addLabelMatcher('{app="web", ns="a"}', 'app', 'api', '!=')).toBe('{ns="a", app!="api"}');
  });
  it('escapes quotes and backslashes', () => {
    expect(addLabelMatcher('', 'path', 'C:\\x "y"')).toBe('{path="C:\\\\x \\"y\\""}');
  });
  it('prefixes a selector when the query has none', () => {
    expect(addLabelMatcher('|= "x"', 'app', 'api')).toBe('{app="api"} |= "x"');
  });
});

describe('splitMatchers', () => {
  it('ignores commas inside quoted values', () => {
    expect(splitMatchers('a="x,y", b=~"1|2"')).toEqual(['a="x,y"', 'b=~"1|2"']);
  });
});

describe('selectorOf', () => {
  it('extracts the leading selector', () => {
    expect(selectorOf(' {a="b"} |= "c"')).toBe('{a="b"}');
    expect(selectorOf('sum(rate({a="b"}[1m]))')).toBe('');
  });
});

describe('line filters and highlighting', () => {
  it('extracts line filter terms', () => {
    expect(lineFilterTerms('{a="b"} |= "timeout" != "debug" |~ `err(or)?`')).toEqual([
      { text: 'timeout', regex: false },
      { text: 'err(or)?', regex: true },
    ]);
  });
  it('splits a line into highlighted segments', () => {
    const segments = highlightSegments('upstream timeout after timeout', [{ text: 'timeout', regex: false }]);
    expect(segments.filter(segment => segment.match).length).toBe(2);
    expect(segments.map(segment => segment.text).join('')).toBe('upstream timeout after timeout');
  });
  it('escapes invalid regular expressions instead of throwing', () => {
    expect(highlightSegments('a(b', [{ text: '(', regex: true }]).some(segment => segment.match)).toBe(true);
  });
});

describe('detectLevel', () => {
  it('prefers labels', () => {
    expect(detectLevel({ level: 'WARNING' }, 'ERROR something')).toBe('warn');
  });
  it('reads logfmt and JSON lines', () => {
    expect(detectLevel({}, 'ts=1 level=error msg=x')).toBe('error');
    expect(detectLevel({}, '{"level":"debug","msg":"x"}')).toBe('debug');
  });
  it('falls back to bare words', () => {
    expect(detectLevel({}, '2024-01-01 INFO started')).toBe('info');
    expect(detectLevel({}, 'nothing here')).toBe('unknown');
  });
});

describe('timestamps', () => {
  it('converts without precision loss', () => {
    expect(msToNs(1_700_000_000_123)).toBe('1700000000123000000');
    expect(nsToMs('1700000000123456789')).toBe(1_700_000_000_123);
    expect(nextNs('1700000000123456789')).toBe('1700000000123456790');
  });
  it('compares nanosecond strings', () => {
    expect(compareNs('999', '1000')).toBeLessThan(0);
    expect(compareNs('1700000000000000002', '1700000000000000001')).toBeGreaterThan(0);
  });
  it('formats with millisecond precision', () => {
    expect(formatTimestamp('1700000000123456789', true)).toBe('2023-11-14 22:13:20.123');
  });
});

describe('misc', () => {
  it('detects metric queries', () => {
    expect(isMetricQuery('sum by (level) (count_over_time({a="b"}[5m]))')).toBe(true);
    expect(isMetricQuery('rate({a="b"}[1m])')).toBe(true);
    expect(isMetricQuery('{a="b"} |= "sum("')).toBe(false);
  });
  it('lists only labels that vary between rows', () => {
    expect(varyingLabels([{ labels: { ns: 'p', app: 'a' } }, { labels: { ns: 'p', app: 'b' } }])).toEqual(['app']);
  });
  it('deduplicates history', () => {
    expect(addHistory(['b', 'a'], ' a ')).toEqual(['a', 'b']);
  });
});
