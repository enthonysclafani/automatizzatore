// Line diff for the builder's "Changes" view and version comparisons.
//
// It uses Myers' O(ND) algorithm ("An O(ND) Difference Algorithm and Its
// Variations", 1986): it finds a shortest edit script, the fewest lines to
// delete and insert to turn the old text into the new one, in time
// proportional to the size of the input times the number of edits. Files
// here are small (a manifest and a few prompts), so a plain implementation
// is enough; MAX_EDITS bounds the work for texts that share almost nothing.

/** One line of a diff. oldNo and newNo are 1-based line numbers in each text. */
export type DiffLine =
  | { kind: "same"; text: string; oldNo: number; newNo: number }
  | { kind: "del"; text: string; oldNo: number }
  | { kind: "add"; text: string; newNo: number };

/** A run of unchanged lines hidden by collapse(). */
export interface DiffGap {
  kind: "gap";
  count: number;
}

export interface FileDiff {
  path: string;
  status: "added" | "removed" | "changed" | "same";
  lines: DiffLine[];
  added: number;
  removed: number;
}

/** Beyond this many edits the middle of the texts is shown as removed, then added. */
const MAX_EDITS = 2000;

/** Splits text into lines; a final newline does not make an extra empty line. */
export function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

/** Edit operations from Myers' algorithm, as indexes into a and b. */
type Op = { kind: "same"; a: number; b: number } | { kind: "del"; a: number } | { kind: "add"; b: number };

/**
 * Shortest edit script from a to b.
 *
 * Picture a grid with a along x and b along y. Moving right deletes a[x],
 * moving down inserts b[y], and a diagonal step is free when a[x] === b[y]
 * (a "snake"). Every path from (0,0) to (n,m) is an edit script; the one with
 * the fewest right and down moves is the shortest.
 *
 * For d = 0, 1, 2... the algorithm keeps, for every diagonal k = x - y that d
 * edits can reach, the furthest x reached on it (v[k]). From d-1 it extends
 * each diagonal by one move (down from k+1 or right from k-1, whichever got
 * further) and then follows the free diagonal steps. The first d whose
 * furthest point reaches (n,m) is the edit distance.
 *
 * To recover the path it saves v for every d ("trace") and walks back from
 * (n,m): at each d it finds which neighbouring diagonal it came from, emits
 * the free steps, then the one insertion or deletion.
 */
function myers(a: string[], b: string[]): Op[] | null {
  const n = a.length;
  const m = b.length;
  const max = Math.min(n + m, MAX_EDITS);
  const offset = max + 1; // v is indexed by k + offset, k in [-max-1, max+1]
  const v = new Int32Array(2 * max + 3);
  // Saved copies of v: trace[d] holds v as it was before step d, for k in [-d, d], at index k + d.
  const trace: Int32Array[] = [];

  for (let d = 0; d <= max; d++) {
    trace.push(v.slice(offset - d, offset + d + 1));
    for (let k = -d; k <= d; k += 2) {
      // Come down from diagonal k+1 (an insertion) or right from k-1 (a deletion).
      const down = k === -d || (k !== d && v[offset + k - 1]! < v[offset + k + 1]!);
      let x = down ? v[offset + k + 1]! : v[offset + k - 1]! + 1;
      let y = x - k;
      // Follow the snake: equal lines cost nothing.
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[offset + k] = x;
      if (x >= n && y >= m) return backtrack(trace, a.length, b.length, d);
    }
  }
  return null; // more than MAX_EDITS edits
}

function backtrack(trace: Int32Array[], n: number, m: number, dEnd: number): Op[] {
  const ops: Op[] = [];
  let x = n;
  let y = m;
  for (let d = dEnd; d > 0; d--) {
    const prev = trace[d]!; // v after step d-1, k at index k + d
    const at = (k: number) => prev[k + d]!;
    const k = x - y;
    const down = k === -d || (k !== d && at(k - 1) < at(k + 1));
    const prevK = down ? k + 1 : k - 1;
    const prevX = at(prevK);
    const prevY = prevX - prevK;
    // The snake after the edit: walk back along equal lines.
    while (x > prevX && y > prevY) {
      x--;
      y--;
      ops.push({ kind: "same", a: x, b: y });
    }
    // The edit itself.
    if (down) {
      y--;
      ops.push({ kind: "add", b: y });
    } else {
      x--;
      ops.push({ kind: "del", a: x });
    }
  }
  // Whatever is left at the start is the snake of d = 0.
  while (x > 0 && y > 0) {
    x--;
    y--;
    ops.push({ kind: "same", a: x, b: y });
  }
  return ops.reverse();
}

/** Line diff of two texts. Common leading and trailing lines are matched first, which keeps the search small. */
export function diffLines(oldText: string, newText: string): DiffLine[] {
  const a = splitLines(oldText);
  const b = splitLines(newText);

  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }

  const out: DiffLine[] = [];
  for (let i = 0; i < start; i++) out.push({ kind: "same", text: a[i]!, oldNo: i + 1, newNo: i + 1 });

  const midA = a.slice(start, endA);
  const midB = b.slice(start, endB);
  const ops = myers(midA, midB) ?? [
    // Too different to search: everything in the middle was replaced.
    ...midA.map((_, i): Op => ({ kind: "del", a: i })),
    ...midB.map((_, i): Op => ({ kind: "add", b: i })),
  ];
  for (const op of ops) {
    if (op.kind === "same") {
      out.push({ kind: "same", text: midA[op.a]!, oldNo: start + op.a + 1, newNo: start + op.b + 1 });
    } else if (op.kind === "del") {
      out.push({ kind: "del", text: midA[op.a]!, oldNo: start + op.a + 1 });
    } else {
      out.push({ kind: "add", text: midB[op.b]!, newNo: start + op.b + 1 });
    }
  }

  for (let i = endA; i < a.length; i++) {
    out.push({ kind: "same", text: a[i]!, oldNo: i + 1, newNo: endB + (i - endA) + 1 });
  }
  return out;
}

/** Keeps `context` unchanged lines around each change and replaces longer unchanged runs with a gap. */
export function collapse(lines: DiffLine[], context = 3): (DiffLine | DiffGap)[] {
  const keep = new Array<boolean>(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.kind === "same") return;
    for (let j = Math.max(0, i - context); j <= Math.min(lines.length - 1, i + context); j++) keep[j] = true;
  });
  const out: (DiffLine | DiffGap)[] = [];
  let hidden = 0;
  lines.forEach((l, i) => {
    if (keep[i]) {
      if (hidden > 0) out.push({ kind: "gap", count: hidden });
      hidden = 0;
      out.push(l);
    } else {
      hidden++;
    }
  });
  if (hidden > 0) out.push({ kind: "gap", count: hidden });
  return out;
}

/** automation.yaml first, then the other paths in order. */
export function sortPaths(paths: string[]): string[] {
  return [...paths].sort((a, b) => {
    if (a === "automation.yaml") return -1;
    if (b === "automation.yaml") return 1;
    return a.localeCompare(b);
  });
}

/** Per-file diff of two sets of files, keyed by path. */
export function diffFiles(before: Record<string, string>, after: Record<string, string>): FileDiff[] {
  const paths = sortPaths([...new Set([...Object.keys(before), ...Object.keys(after)])]);
  return paths.map((path) => {
    const old = before[path];
    const next = after[path];
    const lines = diffLines(old ?? "", next ?? "");
    const added = lines.filter((l) => l.kind === "add").length;
    const removed = lines.filter((l) => l.kind === "del").length;
    const status: FileDiff["status"] =
      old === undefined ? "added" : next === undefined ? "removed" : added + removed === 0 ? "same" : "changed";
    return { path, status, lines, added, removed };
  });
}
