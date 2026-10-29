import { describe, expect, it } from 'vitest';
import {
  addHistory, addLabelMatcher, axisTicks, compareNs, detectLevel, entryKey, formatAxisTime, formatTimestamp, highlightSegments, isMetricQuery,
  lineFilterTerms, LiveCursor, msToNs, nextNs, nsToMs, selectorOf, splitMatchers, varyingLabels,
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
  it('honours RE2 inline flags such as (?i)', () => {
    const terms = lineFilterTerms('{app="api"} |~ "(?i)error"');
    expect(highlightSegments('level=ERROR msg=Error', terms).filter(segment => segment.match).map(segment => segment.text)).toEqual(['ERROR', 'Error']);
  });
  it('keeps each term case-sensitive or not on its own and merges overlaps', () => {
    const terms = [{ text: 'Time', regex: false }, { text: '(?i)timeout', regex: true }];
    const segments = highlightSegments('time TIMEOUT Timeout', terms);
    expect(segments.filter(segment => segment.match).map(segment => segment.text)).toEqual(['TIMEOUT', 'Timeout']);
    expect(segments.map(segment => segment.text).join('')).toBe('time TIMEOUT Timeout');
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
  it('spaces axis ticks evenly and picks a label precision from the span', () => {
    expect(axisTicks(0, 4000)).toEqual([0, 1000, 2000, 3000, 4000]);
    const t = Date.UTC(2026, 9, 29, 9, 5, 7);
    expect(formatAxisTime(t, 60_000, true)).toBe('09:05:07');
    expect(formatAxisTime(t, 3_600_000, true)).toBe('09:05');
    expect(formatAxisTime(t, 7 * 86_400_000, true)).toBe('10-29 09:05');
  });
  it('formats with millisecond precision', () => {
    expect(formatTimestamp('1700000000123456789', true)).toBe('2023-11-14 22:13:20.123');
  });
});

describe('LiveCursor', () => {
  const s = (sec: number) => `${sec}000000000`;
  const api = (sec: number, line: string) => ({ ts: s(sec), line, labels: { app: 'api' } });
  const worker = (sec: number, line: string) => ({ ts: s(sec), line, labels: { app: 'worker' } });

  it('polls from the overlap window before the newest line', () => {
    const cursor = new LiveCursor(s(100));
    expect(cursor.start).toBe(s(90));
    cursor.accept([api(105, 'a')]);
    expect(cursor.start).toBe(s(95));
  });
  it('keeps late lines older than the newest one and skips lines already shown', () => {
    const cursor = new LiveCursor(s(100), [api(99, 'shown')]);
    expect(cursor.accept([api(99, 'shown'), api(104, 'a')])).toEqual([api(104, 'a')]);
    // A batching agent delivers a line from 101 s after the 104 s line was shown.
    expect(cursor.accept([api(104, 'a'), worker(101, 'late')])).toEqual([worker(101, 'late')]);
    expect(cursor.accept([worker(101, 'late'), api(104, 'a')])).toEqual([]);
  });
  it('treats equal timestamps in different streams or with different lines as distinct', () => {
    const cursor = new LiveCursor(s(100));
    expect(cursor.accept([api(101, 'x'), worker(101, 'x'), api(101, 'y')])).toHaveLength(3);
  });
  it('gives a line the same key in every response, whatever its position or stream id', () => {
    const line = { ts: s(101), line: 'x', labels: { b: '2', a: '1' } };
    expect(entryKey({ ...line, stream: 0 } as typeof line)).toBe(entryKey({ ...line, labels: { a: '1', b: '2' }, stream: 7 } as typeof line));
    expect(entryKey(line)).not.toBe(entryKey({ ...line, line: 'y' }));
  });
  it('forgets lines that left the overlap window', () => {
    const cursor = new LiveCursor(s(100), [api(95, 'old')]);
    cursor.accept([api(120, 'new')]);
    expect((cursor as unknown as { seen: Map<string, string> }).seen.size).toBe(1);
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
