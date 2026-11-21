import type { ModuleHost } from '@prusa/connect-ui/slots';

export interface Rule {
  id: string;
  name: string;
  enabled: boolean;
  storage: string;
  path: string;
  mode: 'files' | 'folders';
  depth?: number;
  pattern?: string;
  keep_days: number;
  keep_newest?: number;
}

export interface RulePlan {
  rule_id: string;
  name: string;
  storage: string;
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
  truncated?: boolean;
  error?: string;
}

export interface RunRecord {
  at: string;
  by: string;
  rule: string;
  units: number;
  files: number;
  freed: number;
  error?: string;
  partial?: boolean;
}

export interface State {
  rules: Rule[];
  automatic: boolean;
  interval?: string;
  next_run?: string;
  running: boolean;
  history: RunRecord[];
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

// Die App lädt dieses Bündel von /api/v1/modules/<namespace>/ui/ui/index.js.
// Die eigenen Routen liegen unter /api/v1/modules/<namespace>/. Aus der eigenen
// Adresse abgeleitet, weil ModuleHost.namespace erst nach v0.1.0-beta.1 kam.
const moduleBase = ((): string => {
  const path = new URL(import.meta.url).pathname;
  const cut = path.indexOf('/ui/');

  return cut > 0 ? path.slice(0, cut) : path.replace(/\/[^/]*$/, '');
})();

// eslint-disable-next-line @typescript-eslint/no-unused-vars
const base = (_host: ModuleHost): string => moduleBase;

const call = async <T>(host: ModuleHost, method: string, path: string, body?: unknown): Promise<T> => {
  const init: RequestInit = { method, credentials: 'same-origin' };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { 'Content-Type': 'application/json' };
  }

  const response = await fetch(`${base(host)}${path}`, init);
  const text = await response.text();

  if (!response.ok) {
    let detail = text;
    try {
      detail = (JSON.parse(text) as { detail?: string }).detail ?? text;
    } catch {
      // Kein JSON: der Text selbst ist die Meldung.
    }
    throw new Error(`${response.status}: ${detail}`);
  }

  return (text ? JSON.parse(text) : undefined) as T;
};

export const readState = (host: ModuleHost): Promise<State> => call(host, 'GET', '/overview');

export const writeRules = (host: ModuleHost, rules: Rule[]): Promise<Rule[]> =>
  call(host, 'PUT', '/rules', rules);

export const readPlans = async (host: ModuleHost): Promise<RulePlan[]> =>
  (await call<{ plans: RulePlan[] }>(host, 'GET', '/plan')).plans;

export const readPlan = async (host: ModuleHost, id: string): Promise<RulePlan | undefined> =>
  (await call<{ plans: RulePlan[] }>(host, 'GET', `/plan?rule=${encodeURIComponent(id)}`)).plans[0];

export const readUsage = (host: ModuleHost, storage: string, refresh: boolean): Promise<Usage> =>
  call(host, 'GET', `/usage?storage=${encodeURIComponent(storage)}${refresh ? '&refresh=1' : ''}`);

export const recordRun = (host: ModuleHost, record: Omit<RunRecord, 'at' | 'by'>): Promise<RunRecord> =>
  call(host, 'POST', '/runs', record);

export const runNow = (host: ModuleHost): Promise<{ runs: RunRecord[] }> => call(host, 'POST', '/auto/run');

/** Löscht mit den Rechten der angemeldeten Person, in Stapeln von 200. */
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

export const bytes = (n: number): string => {
  if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(2)} GB`;
  if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(0)} kB`;

  return `${n} B`;
};

export const when = (iso?: string): string => {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 1971) return '';

  return date.toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' });
};
