/**
 * A minimal ANSI SGR parser.
 *
 * Agents draw with escape codes, so a terminal view that printed them literally
 * would be unreadable and one that stripped them would throw away the only
 * structure the output has. This handles the subset agents actually emit —
 * reset, bold, dim, italic, underline, the sixteen basic colours and the 256
 * palette — and **passes anything else through as text** rather than guessing.
 *
 * Deliberately not a terminal emulator: no cursor movement, no alternate screen,
 * no clearing. Those appear in a TUI, and the runs Studio reads are headless
 * agents writing a transcript. A live *interactive* session is attached to with
 * `sandbox-cli attach`, which is a real terminal; this is for reading.
 */

export interface AnsiSpan {
  text: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
  /** A CSS colour, already resolved from the escape code. */
  color?: string;
}

/** The sixteen basic colours, stepped for a dark terminal surface. */
const BASIC = [
  "#3b3b42", // black (lifted, so it is visible at all)
  "#e66767", // red
  "#4ec9a0", // green
  "#e2c08d", // yellow
  "#6da7ec", // blue
  "#d55181", // magenta
  "#4dc4d4", // cyan
  "#d4d4d8", // white
];

const BRIGHT = [
  "#6b6b76",
  "#ff8f8f",
  "#7ee0bb",
  "#f0d9a8",
  "#93c3f5",
  "#e880aa",
  "#7fdfeb",
  "#fafafa",
];

/** xterm-256: 16 basic, a 6×6×6 cube, then 24 greys. */
function color256(n: number): string {
  if (n < 8) return BASIC[n];
  if (n < 16) return BRIGHT[n - 8];
  if (n < 232) {
    const i = n - 16;
    const steps = [0, 95, 135, 175, 215, 255];
    const r = steps[Math.floor(i / 36)];
    const g = steps[Math.floor((i % 36) / 6)];
    const b = steps[i % 6];
    return `rgb(${r} ${g} ${b})`;
  }
  const v = 8 + (n - 232) * 10;
  return `rgb(${v} ${v} ${v})`;
}

const SGR = /\u001b\[([0-9;]*)m/g;

interface State {
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
  color?: string;
}

export function parseAnsi(line: string): AnsiSpan[] {
  const spans: AnsiSpan[] = [];
  let state: State = {};
  let last = 0;

  SGR.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = SGR.exec(line)) !== null) {
    if (match.index > last) {
      spans.push({ text: line.slice(last, match.index), ...state });
    }
    state = applyCodes(state, match[1]);
    last = match.index + match[0].length;
  }
  if (last < line.length) {
    spans.push({ text: line.slice(last), ...state });
  }
  // An empty line still needs a span so it occupies a row.
  return spans.length ? spans : [{ text: "" }];
}

function applyCodes(state: State, raw: string): State {
  const codes = raw === "" ? [0] : raw.split(";").map((c) => parseInt(c, 10) || 0);
  const next: State = { ...state };

  for (let i = 0; i < codes.length; i++) {
    const code = codes[i];
    switch (code) {
      case 0:
        return {};
      case 1:
        next.bold = true;
        break;
      case 2:
        next.dim = true;
        break;
      case 3:
        next.italic = true;
        break;
      case 4:
        next.underline = true;
        break;
      case 22:
        next.bold = false;
        next.dim = false;
        break;
      case 23:
        next.italic = false;
        break;
      case 24:
        next.underline = false;
        break;
      case 39:
        next.color = undefined;
        break;
      case 38: {
        // 38;5;N (256) and 38;2;R;G;B (truecolor).
        if (codes[i + 1] === 5) {
          next.color = color256(codes[i + 2] ?? 7);
          i += 2;
        } else if (codes[i + 1] === 2) {
          next.color = `rgb(${codes[i + 2] ?? 0} ${codes[i + 3] ?? 0} ${codes[i + 4] ?? 0})`;
          i += 4;
        }
        break;
      }
      default:
        if (code >= 30 && code <= 37) next.color = BASIC[code - 30];
        else if (code >= 90 && code <= 97) next.color = BRIGHT[code - 90];
        // Background codes (40–47, 100–107) are recognised and dropped: an agent
        // painting a background over a surface it cannot see is worse than none.
        break;
    }
  }
  return next;
}

/**
 * ANSI stripped, for search and for copying to the clipboard.
 *
 * Broader than {@link parseAnsi}'s pattern on purpose: this drops every CSI
 * sequence rather than only the colour ones, so a stray cursor move cannot
 * survive into a search index as visible junk.
 */
export function stripAnsi(line: string): string {
  return line.replace(/\u001b\[[0-9;?]*[A-Za-z]/g, "");
}
