/**
 * A client for the Agent Client Protocol (ACP), over Studio's bridge to an
 * agentos sandbox's agent (internal/studio/agentos.go): JSON-RPC 2.0, one
 * message a WebSocket frame, wrapped as {"type":"rpc","message":…}.
 *
 * One session per sandbox, kept in a store outside any screen, so moving to
 * the Desktop tab to watch the agent does not end what it is doing. Closing
 * the session (or the page) ends the agent's process.
 *
 * Everything the agent sends is from the guest and untrusted: each field is
 * checked for its type before use, and the screen shows it as text — never as
 * HTML — and an image only as a PNG, JPEG or WebP data URL.
 */
import { create } from "zustand";
import { acpURL } from "@/lib/api/client";

/** The ACP version this client speaks. */
const PROTOCOL_VERSION = 1;
/** Where a session starts: the sandbox user's home, where the agent's login is. */
const SESSION_CWD = "/sandbox/home";
/** How many lines of the agent's log are kept. */
const MAX_LOG = 300;

export type AcpStatus = "connecting" | "ready" | "closed" | "failed";

export interface AcpMode {
  id: string;
  name: string;
  description?: string;
}

/**
 * One of a session's settings, as the agent offers it in ACP's session config
 * options: a mode, a model, a reasoning level. Only selects are shown; a kind
 * this client does not know is left out.
 */
export interface AcpConfigOption {
  id: string;
  name: string;
  category?: string;
  description?: string;
  value: string;
  choices: AcpMode[];
}

export interface AcpPlanEntry {
  content: string;
  status: string;
}

export interface AcpToolCall {
  id: string;
  title: string;
  kind: string;
  status: string;
  text: string[];
  images: string[];
}

export type AcpEntry =
  | { kind: "user"; text: string }
  | { kind: "agent"; text: string }
  | { kind: "thought"; text: string }
  | { kind: "tool"; call: AcpToolCall }
  | { kind: "note"; text: string };

export interface AcpOption {
  id: string;
  name: string;
  kind: string;
}

export interface AcpPermission {
  rpcId: number | string;
  title: string;
  options: AcpOption[];
}

export interface AcpSession {
  sandbox: string;
  agent: string;
  status: AcpStatus;
  error?: string;
  sessionId?: string;
  agentName?: string;
  modes: AcpMode[];
  mode?: string;
  /** The session's config options; an agent that sends them (OpenCode does) has its mode among them, not in modes. */
  options: AcpConfigOption[];
  entries: AcpEntry[];
  plan: AcpPlanEntry[];
  permissions: AcpPermission[];
  busy: boolean;
  log: string[];
}

interface Store {
  sessions: Record<string, AcpSession>;
  patch: (sandbox: string, f: (s: AcpSession) => Partial<AcpSession>) => void;
  drop: (sandbox: string) => void;
}

export const useAcp = create<Store>((set) => ({
  sessions: {},
  patch: (sandbox, f) =>
    set((st) => {
      const cur = st.sessions[sandbox];
      return cur ? { sessions: { ...st.sessions, [sandbox]: { ...cur, ...f(cur) } } } : st;
    }),
  drop: (sandbox) =>
    set((st) => {
      const next = { ...st.sessions };
      delete next[sandbox];
      return { sessions: next };
    }),
}));

/** The live connections, by sandbox: not state a screen draws, so not in the store. */
const conns = new Map<string, Conn>();

const str = (v: unknown): string => (typeof v === "string" ? v : "");
const obj = (v: unknown): Record<string, unknown> => (v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : {});
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const SAFE_IMAGE = /^image\/(png|jpeg|webp)$/;

/**
 * A session's config options, from session/new, a set, or an update. Grouped
 * choices are flattened; an option with nothing to choose from, or whose
 * current value is not one of its choices, is left out rather than shown
 * wrong.
 */
function configOptionsOf(v: unknown): AcpConfigOption[] {
  return arr(v)
    .map(obj)
    .filter((o) => o.type === "select" && str(o.id))
    .map((o) => {
      const choices = arr(o.options)
        .map(obj)
        .flatMap((c) => (c.options === undefined ? [c] : arr(c.options).map(obj)))
        .map((c) => ({ id: str(c.value), name: str(c.name) || str(c.value), description: str(c.description) || undefined }))
        .filter((c) => c.id);
      return {
        id: str(o.id),
        name: str(o.name) || str(o.id),
        category: str(o.category) || undefined,
        description: str(o.description) || undefined,
        value: str(o.currentValue),
        choices,
      };
    })
    .filter((o) => o.choices.some((c) => c.id === o.value));
}

/** Text and images out of ACP content blocks, and the tool-call content that wraps them. */
function contentOf(blocks: unknown): { text: string[]; images: string[] } {
  const text: string[] = [];
  const images: string[] = [];
  const one = (b: Record<string, unknown>) => {
    if (b.type === "text" && str(b.text)) text.push(str(b.text));
    else if (b.type === "image" && SAFE_IMAGE.test(str(b.mimeType)) && /^[A-Za-z0-9+/=]+$/.test(str(b.data)))
      images.push(`data:${str(b.mimeType)};base64,${str(b.data)}`);
  };
  for (const raw of arr(blocks)) {
    const b = obj(raw);
    if (b.type === "content") one(obj(b.content));
    else if (b.type === "diff") text.push(`edited ${str(b.path)}`);
    else if (b.type === "terminal") text.push("(terminal output)");
    else one(b);
  }
  return { text, images };
}

class Conn {
  private ws: WebSocket;
  private nextId = 1;
  private waiting = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  private ended = false;

  constructor(
    private sandbox: string,
    agent: string,
  ) {
    this.ws = new WebSocket(acpURL(sandbox, agent));
    this.ws.onopen = () => void this.start();
    this.ws.onmessage = (ev) => this.receive(ev.data);
    this.ws.onclose = () => this.end();
    this.ws.onerror = () => this.end("the connection to the agent failed");
  }

  private patch(f: (s: AcpSession) => Partial<AcpSession>) {
    useAcp.getState().patch(this.sandbox, f);
  }

  private send(message: Record<string, unknown>) {
    if (this.ws.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify({ type: "rpc", message: { jsonrpc: "2.0", ...message } }));
  }

  request(method: string, params: Record<string, unknown>): Promise<unknown> {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve, reject });
      this.send({ id, method, params });
    });
  }

  notify(method: string, params: Record<string, unknown>) {
    this.send({ method, params });
  }

  answer(id: number | string, result: Record<string, unknown>) {
    this.send({ id, result });
  }

  close() {
    this.ws.close();
    this.end();
  }

  private async start() {
    try {
      const init = obj(
        await this.request("initialize", {
          protocolVersion: PROTOCOL_VERSION,
          // Studio offers the agent no files or terminals of its own: the
          // agent works in the sandbox, with the sandbox's.
          clientCapabilities: { fs: { readTextFile: false, writeTextFile: false }, terminal: false },
        }),
      );
      const info = obj(init.agentInfo);
      const sess = obj(await this.request("session/new", { cwd: SESSION_CWD, mcpServers: [] }));
      const modes = obj(sess.modes);
      this.patch(() => ({
        status: "ready",
        agentName: str(info.title) || str(info.name) || undefined,
        sessionId: str(sess.sessionId),
        modes: arr(modes.availableModes)
          .map(obj)
          .map((m) => ({ id: str(m.id), name: str(m.name) || str(m.id), description: str(m.description) || undefined }))
          .filter((m) => m.id),
        mode: str(modes.currentModeId) || undefined,
        options: configOptionsOf(sess.configOptions),
      }));
    } catch (e) {
      this.end((e as Error).message);
      this.ws.close();
    }
  }

  private receive(data: unknown) {
    let frame: Record<string, unknown>;
    try {
      frame = obj(JSON.parse(String(data)));
    } catch {
      return;
    }
    if (frame.type === "log") {
      const lines = str(frame.data).split("\n").filter(Boolean);
      this.patch((s) => ({ log: [...s.log, ...lines].slice(-MAX_LOG) }));
      return;
    }
    if (frame.type === "exit") {
      this.end(frame.code === 0 ? undefined : `the agent exited with status ${String(frame.code)}`);
      return;
    }
    if (frame.type !== "rpc") return;
    const m = obj(frame.message);
    const method = str(m.method);
    if (!method) {
      // A response to one of ours.
      const w = typeof m.id === "number" ? this.waiting.get(m.id) : undefined;
      if (!w) return;
      this.waiting.delete(m.id as number);
      if (m.error) w.reject(new Error(str(obj(m.error).message) || "the agent refused the request"));
      else w.resolve(m.result);
      return;
    }
    const params = obj(m.params);
    const hasId = typeof m.id === "number" || typeof m.id === "string";
    if (method === "session/update") this.update(obj(params.update));
    else if (method === "session/request_permission" && hasId) {
      const options = arr(params.options)
        .map(obj)
        .map((o) => ({ id: str(o.optionId), name: str(o.name) || str(o.optionId), kind: str(o.kind) }))
        .filter((o) => o.id);
      const title = str(obj(params.toolCall).title) || "a tool call";
      this.patch((s) => ({ permissions: [...s.permissions, { rpcId: m.id as number | string, title, options }] }));
    } else if (hasId) {
      // fs/*, terminal/*: not offered in initialize, so refused.
      this.send({ id: m.id, error: { code: -32601, message: `${method} is not offered by this client` } });
    }
  }

  private update(u: Record<string, unknown>) {
    switch (u.sessionUpdate) {
      case "agent_message_chunk":
      case "agent_thought_chunk": {
        const kind = u.sessionUpdate === "agent_message_chunk" ? "agent" : "thought";
        const text = contentOf([u.content]).text.join("");
        if (!text) return;
        this.patch((s) => {
          const last = s.entries[s.entries.length - 1];
          if (last && last.kind === kind) return { entries: [...s.entries.slice(0, -1), { kind, text: last.text + text }] };
          return { entries: [...s.entries, { kind, text }] };
        });
        return;
      }
      case "tool_call": {
        const c = contentOf(u.content);
        const call: AcpToolCall = {
          id: str(u.toolCallId),
          title: str(u.title) || "a tool",
          kind: str(u.kind) || "other",
          status: str(u.status) || "pending",
          text: c.text,
          images: c.images,
        };
        this.patch((s) => ({ entries: [...s.entries, { kind: "tool", call }] }));
        return;
      }
      case "tool_call_update": {
        const id = str(u.toolCallId);
        const c = u.content === undefined ? null : contentOf(u.content);
        this.patch((s) => ({
          entries: s.entries.map((e) =>
            e.kind === "tool" && e.call.id === id
              ? {
                  kind: "tool",
                  call: {
                    ...e.call,
                    title: str(u.title) || e.call.title,
                    kind: str(u.kind) || e.call.kind,
                    status: str(u.status) || e.call.status,
                    text: c ? c.text : e.call.text,
                    images: c ? c.images : e.call.images,
                  },
                }
              : e,
          ),
        }));
        return;
      }
      case "plan":
        this.patch(() => ({
          plan: arr(u.entries)
            .map(obj)
            .map((e) => ({ content: str(e.content), status: str(e.status) }))
            .filter((e) => e.content),
        }));
        return;
      case "current_mode_update":
        this.patch(() => ({ mode: str(u.currentModeId) || undefined }));
        return;
      case "config_option_update":
        this.patch(() => ({ options: configOptionsOf(u.configOptions) }));
        return;
    }
  }

  private end(error?: string) {
    if (this.ended) return;
    this.ended = true;
    for (const w of this.waiting.values()) w.reject(new Error("the session ended"));
    this.waiting.clear();
    if (conns.get(this.sandbox) === this) conns.delete(this.sandbox);
    this.patch((s) => ({
      status: error || s.status === "connecting" ? "failed" : "closed",
      error: error ?? (s.status === "connecting" ? "the agent could not be reached" : undefined),
      busy: false,
      permissions: [],
    }));
  }
}

/** Starts a session with one of a sandbox's agents, ending any it had. */
export function startSession(sandbox: string, agent: string) {
  conns.get(sandbox)?.close();
  useAcp.setState((st) => ({
    sessions: {
      ...st.sessions,
      [sandbox]: { sandbox, agent, status: "connecting", modes: [], options: [], entries: [], plan: [], permissions: [], busy: false, log: [] },
    },
  }));
  conns.set(sandbox, new Conn(sandbox, agent));
}

/** Ends a sandbox's session; the agent's process exits. */
export function endSession(sandbox: string) {
  conns.get(sandbox)?.close();
}

/** Forgets an ended session, so the tab offers a new one. */
export function clearSession(sandbox: string) {
  if (!conns.has(sandbox)) useAcp.getState().drop(sandbox);
}

/** Sends a prompt and marks the session busy until the agent's turn ends. */
export async function sendPrompt(sandbox: string, text: string) {
  const c = conns.get(sandbox);
  const s = useAcp.getState().sessions[sandbox];
  if (!c || !s?.sessionId || s.busy) return;
  const patch = useAcp.getState().patch;
  patch(sandbox, (cur) => ({ busy: true, entries: [...cur.entries, { kind: "user", text }] }));
  try {
    const res = obj(await c.request("session/prompt", { sessionId: s.sessionId, prompt: [{ type: "text", text }] }));
    const reason = str(res.stopReason);
    if (reason && reason !== "end_turn") patch(sandbox, (cur) => ({ entries: [...cur.entries, { kind: "note", text: `stopped: ${reason.replace(/_/g, " ")}` }] }));
  } catch (e) {
    patch(sandbox, (cur) => ({ entries: [...cur.entries, { kind: "note", text: (e as Error).message }] }));
  } finally {
    patch(sandbox, () => ({ busy: false }));
  }
}

/** Asks the agent to stop its turn; the prompt then ends with "cancelled". */
export function cancelTurn(sandbox: string) {
  const s = useAcp.getState().sessions[sandbox];
  if (s?.sessionId) conns.get(sandbox)?.notify("session/cancel", { sessionId: s.sessionId });
}

/** Switches the session's mode, e.g. to one that asks before each tool call. */
export async function setMode(sandbox: string, mode: string) {
  const c = conns.get(sandbox);
  const s = useAcp.getState().sessions[sandbox];
  if (!c || !s?.sessionId) return;
  await c.request("session/set_mode", { sessionId: s.sessionId, modeId: mode });
  useAcp.getState().patch(sandbox, () => ({ mode }));
}

/**
 * Sets one of the session's config options. The agent answers with all of
 * them, since one choice can change the others (a model's reasoning levels).
 */
export async function setOption(sandbox: string, id: string, value: string) {
  const c = conns.get(sandbox);
  const s = useAcp.getState().sessions[sandbox];
  if (!c || !s?.sessionId) return;
  const res = obj(await c.request("session/set_config_option", { sessionId: s.sessionId, configId: id, value }));
  const options = configOptionsOf(res.configOptions);
  useAcp
    .getState()
    .patch(sandbox, (cur) => ({ options: options.length ? options : cur.options.map((o) => (o.id === id ? { ...o, value } : o)) }));
}

/** Answers a permission request with one of its options, or cancels it (optionId null). */
export function answerPermission(sandbox: string, rpcId: number | string, optionId: string | null) {
  conns
    .get(sandbox)
    ?.answer(rpcId, { outcome: optionId ? { outcome: "selected", optionId } : { outcome: "cancelled" } });
  useAcp.getState().patch(sandbox, (s) => ({ permissions: s.permissions.filter((p) => p.rpcId !== rpcId) }));
}
