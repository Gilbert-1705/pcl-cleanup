<script lang="ts">
  import {
    Badge,
    Button,
    Card,
    CardContent,
    CardHeader,
    CardTitle,
    ConfirmDialog,
  } from '@prusa/connect-ui';

  import {
    bytes,
    deletePaths,
    readPlan,
    readPlans,
    readState,
    readUsage,
    recordRun,
    runNow,
    type Rule,
    type RulePlan,
    type State,
    type Usage,
    when,
    writeRules,
  } from '../api';
  import { heldHost } from '../host';

  const host = heldHost();

  let current = $state<State | undefined>(undefined);
  let plans = $state<Record<string, RulePlan>>({});
  let scanning = $state(false);
  let problem = $state('');
  let notice = $state('');
  let busyRule = $state('');
  let progress = $state('');

  let editing = $state<Rule | undefined>(undefined);
  let editingIndex = $state(-1);

  let storages = $state<string[]>([]);
  let usageStorage = $state('');
  let usage = $state<Usage | undefined>(undefined);
  let usageBusy = $state(false);

  const load = async (): Promise<void> => {
    try {
      current = await readState(host);
      problem = current.error ?? '';
    } catch (error) {
      problem = `Der Zustand konnte nicht gelesen werden: ${String(error)}`;
    }
  };

  const scan = async (): Promise<void> => {
    scanning = true;
    try {
      const list = await readPlans(host);
      plans = Object.fromEntries(list.map((plan) => [plan.rule_id, plan]));
    } catch (error) {
      problem = `Die Ordner konnten nicht durchsucht werden: ${String(error)}`;
    } finally {
      scanning = false;
    }
  };

  const loadUsage = async (refresh: boolean): Promise<void> => {
    if (!usageStorage) return;
    usageBusy = true;
    try {
      usage = await readUsage(host, usageStorage, refresh);
    } catch (error) {
      problem = `Die Speicherbelegung konnte nicht gelesen werden: ${String(error)}`;
    } finally {
      usageBusy = false;
    }
  };

  $effect(() => {
    void load().then(scan);
    void host.client
      .listStorages()
      .then((answer) => {
        storages = (answer.items ?? []).map((s) => s.name);
        usageStorage = storages.includes('local') ? 'local' : (storages[0] ?? '');
        void loadUsage(false);
      })
      .catch(() => {
        storages = [];
      });
  });

  const clean = async (rule: Rule): Promise<void> => {
    busyRule = rule.id;
    notice = '';
    problem = '';
    progress = 'Suche, was weg kann …';

    try {
      const plan = await readPlan(host, rule.id);
      const paths = plan?.delete ?? [];
      if (!plan || paths.length === 0) {
        notice = `${rule.name}: nichts zu löschen.`;

        return;
      }

      let failure = '';
      let done = 0;
      try {
        done = await deletePaths(host, plan.storage, paths, (n) => {
          progress = `${n} von ${paths.length} gelöscht …`;
        });
      } catch (error) {
        failure = String(error);
      }

      const share = plan.delete_units > 0 ? done / plan.delete_units : 0;
      const record = {
        rule: rule.name,
        units: done,
        files: Math.round(plan.delete_files * share),
        freed: Math.round(plan.delete_size * share),
        partial: Boolean(plan.truncated) || done < paths.length,
        ...(failure ? { error: failure } : {}),
      };
      await recordRun(host, record);

      notice = failure
        ? `${rule.name}: ${done} gelöscht, dann Fehler: ${failure}`
        : `${rule.name}: ${done} gelöscht, ${bytes(record.freed)} frei.${record.partial ? ' Es ist noch mehr da, einfach erneut ausführen.' : ''}`;
    } catch (error) {
      problem = String(error);
    } finally {
      busyRule = '';
      progress = '';
      await load();
      await scan();
      await loadUsage(true);
    }
  };

  const runAutomatic = async (): Promise<void> => {
    busyRule = '*';
    try {
      const answer = await runNow(host);
      notice = `Automatischer Lauf: ${answer.runs.length} Regeln ausgeführt.`;
    } catch (error) {
      problem = String(error);
    } finally {
      busyRule = '';
      await load();
      await scan();
    }
  };

  const startEdit = (index: number): void => {
    editingIndex = index;
    editing =
      index >= 0 && current
        ? { ...current.rules[index]! }
        : {
            id: `regel-${Date.now().toString(36)}`,
            name: 'Neue Regel',
            enabled: false,
            storage: storages[0] ?? 'local',
            path: '',
            mode: 'files',
            pattern: '*.jpg',
            keep_days: 14,
            keep_newest: 0,
          };
  };

  const saveRules = async (rules: Rule[]): Promise<boolean> => {
    try {
      await writeRules(host, rules);
      await load();
      await scan();

      return true;
    } catch (error) {
      problem = `Nicht gespeichert: ${String(error)}`;

      return false;
    }
  };

  const saveEdit = async (): Promise<void> => {
    if (!current || !editing) return;
    const rule: Rule = {
      ...editing,
      keep_days: Number(editing.keep_days),
      keep_newest: Number(editing.keep_newest ?? 0),
      depth: editing.mode === 'folders' ? Number(editing.depth ?? 1) : 0,
    };
    const rules = [...current.rules];
    if (editingIndex >= 0) rules[editingIndex] = rule;
    else rules.push(rule);

    if (await saveRules(rules)) editing = undefined;
  };

  const removeRule = async (index: number): Promise<void> => {
    if (!current) return;
    await saveRules(current.rules.filter((_, i) => i !== index));
  };

  const toggle = async (index: number): Promise<void> => {
    if (!current) return;
    const rules = current.rules.map((r, i) => (i === index ? { ...r, enabled: !r.enabled } : r));
    await saveRules(rules);
  };

  const describeKeep = (rule: Rule): string => {
    let text = `älter als ${rule.keep_days} Tage`;
    if (rule.keep_newest) text += `, die neuesten ${rule.keep_newest} bleiben immer`;

    return text;
  };

  const describeWhat = (rule: Rule): string =>
    rule.mode === 'folders'
      ? `Ordner auf Ebene ${rule.depth ?? 1} unter ${rule.path} als Ganzes`
      : `Dateien unter ${rule.path}${rule.pattern ? ` (${rule.pattern})` : ''}`;

  const field =
    'h-8 rounded-sm border border-border bg-background px-2 text-sm text-foreground';

  const share = (size: number): string =>
    usage && usage.size > 0 ? `${Math.max(1, Math.round((size / usage.size) * 100))}%` : '0%';
</script>

<section class="flex flex-col gap-4">
  <header class="flex flex-wrap items-center justify-between gap-3">
    <h1 class="text-lg font-semibold">Aufräumen</h1>
    <div class="flex items-center gap-2">
      <Button variant="secondary" size="sm" disabled={scanning} onclick={() => void scan()}>
        {scanning ? 'Durchsuche …' : 'Neu durchsuchen'}
      </Button>
    </div>
  </header>

  <p class="text-sm text-muted-foreground">
    Kamera-Snapshots, Zeitraffer und Bilder anderer Erweiterungen sammeln sich im Speicher des
    Servers. Jede Regel sagt, was in einem Ordner wie lange bleibt. Gelöscht wird erst, wenn du es
    auslöst, oder automatisch, wenn ein API-Token hinterlegt ist.
  </p>

  {#if problem}<p aria-live="polite" class="text-sm text-destructive-text">{problem}</p>{/if}
  {#if notice}<p aria-live="polite" class="text-sm">{notice}</p>{/if}
  {#if progress}<p class="text-sm text-muted-foreground">{progress}</p>{/if}

  <Card>
    <CardHeader>
      <CardTitle>Regeln</CardTitle>
    </CardHeader>
    <CardContent>
      {#if !current}
        <p class="text-sm text-muted-foreground">Lade …</p>
      {:else}
        <ul class="flex flex-col gap-3">
          {#each current.rules as rule, index (rule.id)}
            {@const plan = plans[rule.id]}
            <li class="flex flex-col gap-2 border-b border-border pb-3">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium">{rule.name}</span>
                <Badge variant={rule.enabled ? 'success' : 'secondary'}>
                  {rule.enabled ? 'aktiv' : 'aus'}
                </Badge>
                <span class="text-xs text-muted-foreground">{rule.storage}: {describeWhat(rule)}</span>
              </div>
              <p class="text-sm text-muted-foreground">Löschen: {describeKeep(rule)}</p>

              {#if plan?.error}
                <p class="text-sm text-destructive-text">{plan.error}</p>
              {:else if plan}
                <p class="text-sm">
                  {plan.units}
                  {rule.mode === 'folders' ? 'Ordner' : 'Dateien'}, {bytes(plan.size)}
                  {#if plan.oldest && when(plan.oldest)}· ältestes vom {when(plan.oldest)}{/if}
                  · <strong>
                    {plan.delete_units} zu löschen ({bytes(plan.delete_size)})
                  </strong>
                  {#if plan.truncated}· nur ein Teil durchsucht{/if}
                </p>
              {:else if scanning}
                <p class="text-sm text-muted-foreground">Durchsuche …</p>
              {/if}

              <div class="flex flex-wrap gap-2">
                <ConfirmDialog
                  title={`${rule.name} aufräumen?`}
                  description={`${plan?.delete_units ?? 'Alle'} ${rule.mode === 'folders' ? 'Ordner' : 'Dateien'} ${describeKeep(rule)} unter ${rule.storage}:${rule.path} werden endgültig gelöscht${plan ? ` (${bytes(plan.delete_size)})` : ''}.`}
                  confirmLabel="Löschen"
                  variant="destructive"
                  onConfirm={() => void clean(rule)}
                >
                  {#snippet trigger({ props })}
                    <Button
                      {...props}
                      variant="destructive"
                      size="sm"
                      disabled={busyRule !== '' || (plan !== undefined && plan.delete_units === 0)}
                      pending={busyRule === rule.id}
                    >
                      Jetzt aufräumen
                    </Button>
                  {/snippet}
                </ConfirmDialog>
                <Button variant="secondary" size="sm" onclick={() => void toggle(index)}>
                  {rule.enabled ? 'Ausschalten' : 'Einschalten'}
                </Button>
                <Button variant="ghost" size="sm" onclick={() => startEdit(index)}>Bearbeiten</Button>
                <ConfirmDialog
                  title="Regel entfernen?"
                  description={`Die Regel „${rule.name}“ wird entfernt. Dateien werden dabei nicht gelöscht.`}
                  confirmLabel="Entfernen"
                  variant="destructive"
                  onConfirm={() => void removeRule(index)}
                >
                  {#snippet trigger({ props })}
                    <Button {...props} variant="ghost" size="sm">Entfernen</Button>
                  {/snippet}
                </ConfirmDialog>
              </div>
            </li>
          {/each}
        </ul>

        {#if editing}
          <form
            class="mt-4 grid gap-3 rounded-md border border-border p-3 @md:grid-cols-2"
            onsubmit={(event) => {
              event.preventDefault();
              void saveEdit();
            }}
          >
            <label class="flex flex-col gap-1 text-sm">
              Name
              <input class={field} bind:value={editing.name} required maxlength="80" />
            </label>
            <label class="flex flex-col gap-1 text-sm">
              Speicher
              <select class={field} bind:value={editing.storage}>
                {#each storages.length ? storages : [editing.storage] as name (name)}
                  <option value={name}>{name}</option>
                {/each}
              </select>
            </label>
            <label class="flex flex-col gap-1 text-sm">
              Ordner (nie die oberste Ebene)
              <input class={field} bind:value={editing.path} required placeholder="z. B. camera" />
            </label>
            <label class="flex flex-col gap-1 text-sm">
              Was wird beurteilt
              <select class={field} bind:value={editing.mode}>
                <option value="files">einzelne Dateien</option>
                <option value="folders">ganze Ordner (z. B. ein Zeitraffer)</option>
              </select>
            </label>
            {#if editing.mode === 'files'}
              <label class="flex flex-col gap-1 text-sm">
                Nur Dateien wie (leer = alle)
                <input class={field} bind:value={editing.pattern} placeholder="*.jpg" />
              </label>
            {:else}
              <label class="flex flex-col gap-1 text-sm">
                Ordnerebene unter dem Ordner
                <input class={field} type="number" min="1" max="5" bind:value={editing.depth} />
              </label>
            {/if}
            <label class="flex flex-col gap-1 text-sm">
              Behalten (Tage)
              <input class={field} type="number" min="1" max="3650" bind:value={editing.keep_days} required />
            </label>
            <label class="flex flex-col gap-1 text-sm">
              Die neuesten immer behalten (Anzahl)
              <input class={field} type="number" min="0" bind:value={editing.keep_newest} />
            </label>
            <label class="flex items-center gap-2 text-sm">
              <input type="checkbox" bind:checked={editing.enabled} />
              aktiv (für den automatischen Lauf)
            </label>
            <div class="flex gap-2 @md:col-span-2">
              <Button type="submit" size="sm">Speichern</Button>
              <Button variant="ghost" size="sm" onclick={() => (editing = undefined)}>Abbrechen</Button>
            </div>
          </form>
        {:else}
          <div class="mt-3">
            <Button variant="secondary" size="sm" onclick={() => startEdit(-1)}>Regel hinzufügen</Button>
          </div>
        {/if}
      {/if}
    </CardContent>
  </Card>

  <Card>
    <CardHeader>
      <CardTitle>Automatisch</CardTitle>
    </CardHeader>
    <CardContent>
      {#if current?.automatic}
        <p class="text-sm">
          Alle {current.interval} werden die aktiven Regeln ausgeführt.
          {#if when(current.next_run)}Nächster Lauf: {when(current.next_run)}.{/if}
        </p>
        <div class="mt-2">
          <Button
            variant="secondary"
            size="sm"
            disabled={busyRule !== ''}
            pending={busyRule === '*'}
            onclick={() => void runAutomatic()}>Jetzt ausführen</Button
          >
        </div>
      {:else}
        <p class="text-sm text-muted-foreground">
          Aus. Zum Einschalten unter Einstellungen → API-Zugangsdaten einen Zugang mit dem Umfang
          „operate“ anlegen und ihn unter Einstellungen → Erweiterungen → Aufräumen als api_token
          eintragen, dazu auto_interval (z. B. 6h). Bis dahin löscht nur der Knopf „Jetzt aufräumen“.
        </p>
      {/if}
    </CardContent>
  </Card>

  <Card>
    <CardHeader>
      <CardTitle>Speicherbelegung</CardTitle>
    </CardHeader>
    <CardContent>
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <select
          class={field}
          bind:value={usageStorage}
          onchange={() => void loadUsage(false)}
          aria-label="Speicher"
        >
          {#each storages as name (name)}
            <option value={name}>{name}</option>
          {/each}
        </select>
        <Button variant="ghost" size="sm" disabled={usageBusy} onclick={() => void loadUsage(true)}>
          {usageBusy ? 'Zähle …' : 'Neu zählen'}
        </Button>
        {#if usage}
          <span class="text-sm text-muted-foreground">
            {bytes(usage.size)} in {usage.files} Dateien · Stand {when(usage.scanned)}
          </span>
        {/if}
      </div>
      {#if usage}
        <ul class="flex flex-col gap-2">
          {#each usage.items as item (item.path)}
            <li class="flex flex-col gap-1">
              <div class="flex justify-between gap-2 text-sm">
                <span>{item.dir ? `${item.path}/` : item.path}</span>
                <span class="text-muted-foreground">{bytes(item.size)} · {item.files} Dateien</span>
              </div>
              <div class="h-1.5 w-full rounded-full bg-muted">
                <div class="h-1.5 rounded-full bg-primary" style:width={share(item.size)}></div>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </CardContent>
  </Card>

  <Card>
    <CardHeader>
      <CardTitle>Verlauf</CardTitle>
    </CardHeader>
    <CardContent>
      {#if !current || current.history.length === 0}
        <p class="text-sm text-muted-foreground">Noch nichts gelöscht.</p>
      {:else}
        <ul class="flex flex-col gap-1">
          {#each current.history as run, i (i)}
            <li class="text-sm">
              <span class="text-muted-foreground">{when(run.at)}</span>
              · {run.rule} · {run.units} gelöscht · {bytes(run.freed)} frei · {run.by}
              {#if run.partial}<Badge variant="warning">unvollständig</Badge>{/if}
              {#if run.error}<span class="text-destructive-text"> · {run.error}</span>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    </CardContent>
  </Card>
</section>
