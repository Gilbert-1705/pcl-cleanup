import { formatBytes, formatDateTime } from '@prusa/connect-ui/i18n';
import type { ModuleHost } from '@prusa/connect-ui/slots';

export interface Rule {
  id: string;
  name: string;
  enabled: boolean;
  /**
   * A printer rule the reader may not see in full: it comes with id, name and
   * state only. Send it back unchanged; the server keeps the stored rule.
   */
  hidden?: boolean;
  /** 'storage' (the default) or 'printer' for a printer's own storage. */
  target?: 'storage' | 'printer';
  storage?: string;
  device?: string;
  path: string;
  mode: 'files' | 'folders';
  depth?: number;
  pattern?: string;
  keep_days: number;
  keep_newest?: number;
  /** Printer rule: the server storage that holds the copies of the print files. */
  copy_storage?: string;
  /** Printer rule: the folder with the copies, empty for the whole storage. */
  copy_path?: string;
  /** Printer rule: no copy check (by hand only). By default the check is on. */
  skip_copy_check?: boolean;
  /** Who last saved the rule with a change. Only the server sets it. */
  saved_by?: string;
}

export interface RulePlan {
  rule_id: string;
  name: string;
  target: 'storage' | 'printer';
  storage?: string;
  device?: string;
  path: string;
  enabled: boolean;
  units: number;
  files: number;
  size: number;
  oldest?: string;
  delete_units: number;
  delete_files: number;
  delete_size: number;
  delete?: string[];
  /** Printer rule: files age and count would delete, but that have no copy. */
  kept_no_copy?: number;
  kept_no_copy_size?: number;
  kept_no_copy_list?: string[];
  /** The plan only deletes files with a copy on the server. */
  copy_check?: boolean;
  truncated?: boolean;
  /** A printer rule the overview does not read. Ask for it alone. */
  lazy?: boolean;
  error?: string;
}

export interface RunRecord {
  at: string;
  /** 'schedule', 'request', or 'hand:' followed by the person. Older runs may hold free text. */
  by: string;
  rule: string;
  units: number;
  files: number;
  freed: number;
  error?: string;
  partial?: boolean;
  /** Automatic printer run: each deleted file, in a dry run each one that would have been. */
  deleted?: string[];
  dry_run?: boolean;
}

export interface State {
  rules: Rule[];
  /** Names the stored rules; a save sends it back (writeRules). */
  rules_version: string;
  /** The interval of the automatic printer run, when auto_interval is set. */
  interval?: string;
  next_run?: string;
  running: boolean;
  history: RunRecord[];
  /** Why the last automatic printer run left a rule out, by rule id. */
  skipped?: Record<string, string>;
  allowed_storages: string[] | null;
  allowed_devices: string[] | null;
  /** Printer rules may be active (printer_auto). */
  printer_auto: boolean;
  /** … and they also run, because auto_interval is set. */
  printer_automatic: boolean;
  printer_auto_max: number;
  printer_auto_dry_run: boolean;
  error?: string;
}

export interface UsageItem {
  path: string;
  dir: boolean;
  size: number;
  files: number;
  newest?: string;
}

export interface Usage {
  storage: string;
  size: number;
  files: number;
  items: UsageItem[];
  scanned: string;
  truncated?: boolean;
}

// The app loads this bundle from /api/v1/modules/<namespace>/ui/ui/index.js.
// The module's own routes are under /api/v1/modules/<namespace>/. Derived from
// the bundle's own address, as ModuleHost.namespace came after v0.1.0-beta.1.
const moduleBase = ((): string => {
  const path = new URL(import.meta.url).pathname;
  const cut = path.indexOf('/ui/');

  return cut > 0 ? path.slice(0, cut) : path.replace(/\/[^/]*$/, '');
})();

// eslint-disable-next-line @typescript-eslint/no-unused-vars
const base = (_host: ModuleHost): string => moduleBase;

/** An answer of the module that was not a success, with its HTTP status. */
export class CallError extends Error {
  constructor(
    readonly status: number,
    detail: string,
  ) {
    super(`${status}: ${detail}`);
  }
}

// Every route that changes something takes JSON only, so a body is always
// sent as JSON, an empty object where the route needs none.
const call = async <T>(
  host: ModuleHost,
  method: string,
  path: string,
  body?: unknown,
  headers: Record<string, string> = {},
): Promise<T> => {
  const init: RequestInit = { method, credentials: 'same-origin' };
  if (method !== 'GET') {
    init.body = JSON.stringify(body ?? {});
    init.headers = { ...headers, 'Content-Type': 'application/json' };
  }

  const response = await fetch(`${base(host)}${path}`, init);
  const text = await response.text();

  if (!response.ok) {
    let detail = text;
    try {
      detail = (JSON.parse(text) as { detail?: string }).detail ?? text;
    } catch {
      // Not JSON: the text itself is the message.
    }
    throw new CallError(response.status, detail);
  }

  return (text ? JSON.parse(text) : undefined) as T;
};

export const readState = (host: ModuleHost): Promise<State> => call(host, 'GET', '/overview');

/**
 * Saves the rules, made on the list the overview named with version. A list
 * that changed since answers 412 (CallError).
 */
export const writeRules = (
  host: ModuleHost,
  rules: Rule[],
  version: string,
): Promise<{ rules: Rule[]; rules_version: string }> =>
  call(host, 'PUT', '/rules', rules, { 'If-Match': `"${version}"` });

export const readPlans = async (host: ModuleHost): Promise<RulePlan[]> =>
  (await call<{ plans: RulePlan[] }>(host, 'GET', '/plan')).plans;

export const readPlan = async (host: ModuleHost, id: string): Promise<RulePlan | undefined> =>
  (await call<{ plans: RulePlan[] }>(host, 'GET', `/plan?rule=${encodeURIComponent(id)}`)).plans[0];

export const readUsage = (host: ModuleHost, storage: string, refresh: boolean): Promise<Usage> =>
  call(host, 'GET', `/usage?storage=${encodeURIComponent(storage)}${refresh ? '&refresh=1' : ''}`);

export interface Folders {
  storage: string;
  path: string;
  folders: string[];
  truncated?: boolean;
}

export const readFolders = (host: ModuleHost, storage: string, path: string): Promise<Folders> =>
  call(host, 'GET', `/folders?storage=${encodeURIComponent(storage)}&path=${encodeURIComponent(path)}`);

export const recordRun = (host: ModuleHost, record: Omit<RunRecord, 'at' | 'by'>): Promise<RunRecord> =>
  call(host, 'POST', '/runs', record);

export const runNow = (host: ModuleHost): Promise<{ runs: RunRecord[] }> => call(host, 'POST', '/auto/run');

/** Deletes with the signed-in person's rights, in batches of 200. */
export const deletePaths = async (
  host: ModuleHost,
  storage: string,
  paths: string[],
  progress: (done: number) => void,
): Promise<number> => {
  let done = 0;

  for (let start = 0; start < paths.length; start += 200) {
    const batch = paths.slice(start, start + 200);
    await host.client.deleteStorageEntries({ path: { storage }, body: { paths: batch } });
    done += batch.length;
    progress(done);
  }

  return done;
};

/**
 * Deletes print files from a printer's own storage, one after the other and
 * with the signed-in person's rights. The printer answers one command at a
 * time, so not in parallel.
 */
export const deletePrinterFiles = async (
  host: ModuleHost,
  device: string,
  paths: string[],
  progress: (done: number) => void,
): Promise<number> => {
  let done = 0;

  for (const path of paths) {
    await host.client.deleteDeviceFile({ path: { device_id: device }, query: { path } });
    done += 1;
    progress(done);
  }

  return done;
};

/** Whether a list from the settings lets a name in. "*" lets everything in. */
export const allowed = (list: string[] | null | undefined, name: string): boolean =>
  (list ?? []).includes('*') || (list ?? []).includes(name);

/** A size in the reader's language. */
export const bytes = (n: number): string => formatBytes(n);

/** A point in time in the reader's language, or '' for none. */
export const when = (iso?: string): string => {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 1971) return '';

  return formatDateTime(date);
};
