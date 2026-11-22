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
    allowed,
    bytes,
    deletePaths,
    deletePrinterFiles,
    type Folders,
    readFolders,
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
  let printers = $state<{ id: string; name: string }[]>([]);
  let reading = $state('');
  let usageStorage = $state('');
  let usage = $state<Usage | undefined>(undefined);
  let usageBusy = $state(false);

  // Ordnerauswahl für die Kopien einer Drucker-Regel.
  let picker = $state<Folders | undefined>(undefined);
  let pickerBusy = $state(false);

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

  // Nur was die Einstellungen der Erweiterung zulassen (allowed_storages,
  // allowed_devices), und bei Druckern nur die, die die angemeldete Person
  // bedienen darf: Gelöscht wird dort mit ihren Rechten.
  const loadPlaces = async (): Promise<void> => {
    try {
      const answer = await host.client.listStorages();
      storages = (answer.items ?? [])
        .map((s) => s.name)
        .filter((name) => allowed(current?.allowed_storages, name));
      usageStorage = storages.includes('local') ? 'local' : (storages[0] ?? '');
      void loadUsage(false);
    } catch {
      storages = [];
    }

    if ((current?.allowed_devices ?? []).length === 0) {
      printers = [];

      return;
    }

    try {
      const answer = await host.client.listDevices({ query: { limit: 200 } });
      printers = (answer.items ?? [])
        .filter((d) => allowed(current?.allowed_devices, d.id) && (d.actions ?? []).includes('operate'))
        .map((d) => ({ id: d.id, name: d.name || d.model || d.id }));
    } catch {
      printers = [];
    }
  };

  $effect(() => {
    void load().then(async () => {
      await loadPlaces();
      await scan();
    });
  });

  const printerName = (id: string | undefined): string =>
    printers.find((p) => p.id === id)?.name ?? id ?? '';

  // Den Drucker lesen dauert und belegt ihn, deshalb nur auf Knopfdruck.
  const readPrinter = async (rule: Rule): Promise<void> => {
    reading = rule.id;
    try {
      const plan = await readPlan(host, rule.id);
      if (plan) plans = { ...plans, [rule.id]: plan };
    } catch (error) {
      problem = String(error);
    } finally {
      reading = '';
    }
  };

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
      const step = (n: number): void => {
        progress = `${n} von ${paths.length} gelöscht …`;
      };
      try {
        done =
          plan.target === 'printer'
            ? await deletePrinterFiles(host, plan.device ?? '', paths, step)
            : await deletePaths(host, plan.storage ?? '', paths, step);
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

  const openFolder = async (path: string): Promise<void> => {
    if (!editing?.copy_storage) return;
    pickerBusy = true;
    try {
      picker = await readFolders(host, editing.copy_storage, path);
    } catch (error) {
      problem = `Die Ordner konnten nicht gelesen werden: ${String(error)}`;
    } finally {
      pickerBusy = false;
    }
  };

  const parentOf = (path: string): string => path.split('/').slice(0, -1).join('/');

  const takeFolder = (path: string): void => {
    if (editing) editing = { ...editing, copy_path: path };
    picker = undefined;
  };

  // Kopie-Abgleich ein- oder ausschalten. Aus heißt: keine Angaben zum Kopie-Speicher.
  const chooseCopyCheck = (on: boolean): void => {
    if (!editing) return;
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { copy_storage, copy_path, skip_copy_check, ...rest } = editing;
    // Ohne Kopie-Abgleich läuft eine Drucker-Regel nie automatisch.
    editing = on
      ? { ...rest, copy_storage: copy_storage || defaultCopyStorage(), copy_path: copy_path ?? '' }
      : { ...rest, skip_copy_check: true, enabled: false };
    picker = undefined;
  };

  const defaultCopyStorage = (): string => (storages.includes('local') ? 'local' : (storages[0] ?? ''));

  const startEdit = (index: number): void => {
    picker = undefined;
    editingIndex = index;
    editing =
      index >= 0 && current
        ? { target: 'storage', ...current.rules[index]! }
        : {
            id: `regel-${Date.now().toString(36)}`,
            name: 'Neue Regel',
            enabled: false,
            target: 'storage',
            storage: storages[0] ?? 'local',
            path: '',
            mode: 'files',
            pattern: '*.jpg',
            keep_days: 14,
            keep_newest: 0,
          };
  };

  // Beim Wechsel auf den Drucker-Speicher die Vorgaben, die eine Drucker-Regel
  // braucht: nur Druckdateien, einzeln, nur von Hand, nur mit Kopie auf dem
  // Server. Vorgabe: die neuesten 10 behalten, ohne Altersgrenze.
  const chooseTarget = (target: 'storage' | 'printer'): void => {
    if (!editing) return;
    picker = undefined;
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { storage, device, copy_storage, copy_path, skip_copy_check, ...rest } = editing;
    editing =
      target === 'printer'
        ? {
            ...rest,
            target,
            device: printers[0]?.id ?? '',
            path: 'usb',
            mode: 'files',
            pattern: '*.bgcode',
            enabled: false,
            keep_days: 0,
            keep_newest: 10,
            copy_storage: defaultCopyStorage(),
            copy_path: '',
          }
        : {
            ...rest,
            target,
            storage: storages[0] ?? 'local',
            path: '',
            pattern: '*.jpg',
            keep_days: Math.max(1, Number(rest.keep_days)),
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
    // saved_by setzt der Server, nie die Seite.
    delete rule.saved_by;
    if (!rule.skip_copy_check) delete rule.skip_copy_check;
    const rules = [...current.rules];
    if (editingIndex >= 0) rules[editingIndex] = rule;
    else rules.push(rule);

    if (await saveRules(rules)) {
      editing = undefined;
      picker = undefined;
    }
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
    let text =
      rule.keep_days === 0
        ? `alles außer den neuesten ${rule.keep_newest ?? 0}`
        : `älter als ${rule.keep_days} Tage`;
    if (rule.keep_days !== 0 && rule.keep_newest) text += `, die neuesten ${rule.keep_newest} bleiben immer`;
    if (rule.target === 'printer') {
      text += rule.skip_copy_check
        ? ', ohne Kopie-Abgleich'
        : `, nur mit Kopie in ${rule.copy_storage ?? '?'}:${rule.copy_path || '(ganzer Speicher)'}`;
    }

    return text;
  };

  const describeWhat = (rule: Rule): string =>
    rule.target === 'printer'
      ? `Druckdateien in ${rule.path} (${rule.pattern ?? ''}), ohne Unterordner`
      : rule.mode === 'folders'
        ? `Ordner auf Ebene ${rule.depth ?? 1} unter ${rule.path} als Ganzes`
        : `Dateien unter ${rule.path}${rule.pattern ? ` (${rule.pattern})` : ''}`;

  const describeWhere = (rule: Rule): string =>
    rule.target === 'printer' ? `Drucker ${printerName(rule.device)}` : (rule.storage ?? '');

  const unitWord = (rule: Rule): string => (rule.mode === 'folders' ? 'Ordner' : 'Dateien');

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
                {#if rule.target === 'printer' && !rule.enabled}
                  <Badge variant="secondary">nur von Hand</Badge>
                {:else if rule.target === 'printer'}
                  <Badge variant="success">aktiv</Badge>
                  {#if current.printer_auto_dry_run}<Badge variant="warning">Probelauf</Badge>{/if}
                {:else}
                  <Badge variant={rule.enabled ? 'success' : 'secondary'}>
                    {rule.enabled ? 'aktiv' : 'aus'}
                  </Badge>
                {/if}
                <span class="text-xs text-muted-foreground">{describeWhere(rule)}: {describeWhat(rule)}</span>
              </div>
              <p class="text-sm text-muted-foreground">Löschen: {describeKeep(rule)}</p>
              {#if rule.target === 'printer' && rule.enabled && !current.printer_auto}
                <p class="text-sm text-warning-text">
                  Aktiv, aber printer_auto ist in den Einstellungen aus. Läuft nur von Hand.
                </p>
              {/if}
              {#if current.automatic && rule.enabled && rule.target !== 'printer' && !rule.saved_by}
                <p class="text-sm text-muted-foreground">
                  Läuft erst automatisch, wenn jemand mit Löschrecht die Regel einmal speichert.
                </p>
              {/if}
              {#if current.skipped?.[rule.id]}
                <p class="text-sm text-warning-text">Beim letzten automatischen Lauf ausgelassen: {current.skipped[rule.id]}</p>
              {/if}

              {#if plan?.error}
                <p class="text-sm text-destructive-text">{plan.error}</p>
              {:else if plan?.lazy}
                <p class="text-sm text-muted-foreground">
                  Noch nicht gelesen. Den Drucker zu lesen dauert etwas und geht nicht während eines Drucks.
                </p>
              {:else if plan}
                <p class="text-sm">
                  {plan.units}
                  {unitWord(rule)}, {bytes(plan.size)}
                  {#if plan.oldest && when(plan.oldest)}· ältestes vom {when(plan.oldest)}{/if}
                  · <strong>
                    {plan.delete_units} zu löschen ({bytes(plan.delete_size)})
                  </strong>
                  {#if plan.truncated}· nur ein Teil durchsucht{/if}
                </p>
                {#if rule.target === 'printer'}
                  <ul class="text-sm text-muted-foreground">
                    <li>Behalten, die neuesten: {Math.min(rule.keep_newest ?? 0, plan.units)}</li>
                    {#if plan.copy_check}
                      <li>
                        Behalten, keine Kopie in Connect Local: {plan.kept_no_copy ?? 0}
                        {#if plan.kept_no_copy}({bytes(plan.kept_no_copy_size ?? 0)}){/if}
                      </li>
                    {/if}
                    <li>Löschen: {plan.delete_units}</li>
                  </ul>
                  {#if plan.kept_no_copy_list?.length}
                    <details class="text-sm">
                      <summary class="cursor-pointer text-muted-foreground">Ohne Kopie, bleiben liegen</summary>
                      <ul class="mt-1 max-h-48 overflow-auto">
                        {#each plan.kept_no_copy_list as name (name)}<li>{name}</li>{/each}
                      </ul>
                    </details>
                  {/if}
                {/if}
              {:else if scanning}
                <p class="text-sm text-muted-foreground">Durchsuche …</p>
              {/if}

              <div class="flex flex-wrap gap-2">
                {#if rule.target === 'printer'}
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={reading !== '' || busyRule !== ''}
                    pending={reading === rule.id}
                    onclick={() => void readPrinter(rule)}
                  >
                    Drucker lesen
                  </Button>
                {/if}
                <ConfirmDialog
                  title={`${rule.name} aufräumen?`}
                  description={`${plan?.delete_units ?? 'Alle'} ${unitWord(rule)} ${describeKeep(rule)} unter ${describeWhere(rule)}:${rule.path} werden endgültig gelöscht${plan ? ` (${bytes(plan.delete_size)})` : ''}.${rule.target === 'printer' ? ` Vorher wird der Drucker noch einmal gelesen${rule.skip_copy_check ? '' : ' und jede Datei mit den Kopien auf dem Server abgeglichen'}.` : ''}`}
                  confirmLabel="Löschen"
                  variant="destructive"
                  onConfirm={() => void clean(rule)}
                >
                  {#snippet trigger({ props })}
                    <Button
                      {...props}
                      variant="destructive"
                      size="sm"
                      disabled={busyRule !== '' ||
                        (plan !== undefined && (plan.lazy || Boolean(plan.error) || plan.delete_units === 0))}
                      pending={busyRule === rule.id}
                    >
                      Jetzt aufräumen
                    </Button>
                  {/snippet}
                </ConfirmDialog>
                {#if rule.target !== 'printer' || rule.enabled || (current.printer_auto && !rule.skip_copy_check)}
                  <Button variant="secondary" size="sm" onclick={() => void toggle(index)}>
                    {rule.enabled ? 'Ausschalten' : 'Einschalten'}
                  </Button>
                {/if}
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
            {#if printers.length > 0 || editing.target === 'printer'}
              <label class="flex flex-col gap-1 text-sm">
                Wo
                <select
                  class={field}
                  value={editing.target ?? 'storage'}
                  onchange={(event) => chooseTarget(event.currentTarget.value as 'storage' | 'printer')}
                >
                  <option value="storage">Speicher des Servers</option>
                  <option value="printer">Speicher eines Druckers (USB)</option>
                </select>
              </label>
            {/if}
            {#if editing.target === 'printer'}
              <label class="flex flex-col gap-1 text-sm">
                Drucker
                <select class={field} bind:value={editing.device} required>
                  {#each printers.length ? printers : [{ id: editing.device ?? '', name: editing.device ?? '' }] as printer (printer.id)}
                    <option value={printer.id}>{printer.name}</option>
                  {/each}
                </select>
              </label>
              <label class="flex flex-col gap-1 text-sm">
                Ordner auf dem Drucker (ohne Unterordner)
                <input class={field} bind:value={editing.path} required placeholder="usb" />
              </label>
              <label class="flex flex-col gap-1 text-sm">
                Nur Druckdateien wie
                <input class={field} bind:value={editing.pattern} required placeholder="*.bgcode" />
              </label>
              <label class="flex items-center gap-2 text-sm @md:col-span-2">
                <input
                  type="checkbox"
                  checked={!editing.skip_copy_check}
                  onchange={(event) => chooseCopyCheck(event.currentTarget.checked)}
                />
                Nur löschen, was mit gleichem Namen und gleicher Größe auch in Connect Local liegt (empfohlen)
              </label>
              {#if !editing.skip_copy_check}
                <label class="flex flex-col gap-1 text-sm">
                  Kopien liegen im Speicher
                  <select
                    class={field}
                    bind:value={editing.copy_storage}
                    onchange={() => {
                      if (editing) editing = { ...editing, copy_path: '' };
                      picker = undefined;
                    }}
                    required
                  >
                    {#each storages.length ? storages : [editing.copy_storage ?? ''] as name (name)}
                      <option value={name}>{name}</option>
                    {/each}
                  </select>
                </label>
                <div class="flex flex-col gap-1 text-sm">
                  <span>Ordner mit den Kopien (mit Unterordnern)</span>
                  <div class="flex flex-wrap items-center gap-2">
                    <code class="rounded-sm bg-muted px-2 py-1">{editing.copy_path || '(ganzer Speicher)'}</code>
                    <Button
                      variant="secondary"
                      size="sm"
                      pending={pickerBusy}
                      onclick={() => void openFolder(editing?.copy_path ?? '')}
                    >
                      Ordner wählen
                    </Button>
                  </div>
                </div>
                {#if picker}
                  <div class="flex flex-col gap-1 rounded-sm border border-border p-2 text-sm @md:col-span-2">
                    <div class="flex flex-wrap items-center gap-2">
                      <span class="text-muted-foreground">{picker.storage}:</span>
                      <code>{picker.path || '/'}</code>
                      <Button size="sm" onclick={() => takeFolder(picker?.path ?? '')}>Diesen Ordner nehmen</Button>
                      {#if picker.path}
                        <Button variant="ghost" size="sm" onclick={() => void openFolder(parentOf(picker?.path ?? ''))}>
                          Eine Ebene hoch
                        </Button>
                      {/if}
                      <Button variant="ghost" size="sm" onclick={() => (picker = undefined)}>Schließen</Button>
                    </div>
                    {#if picker.folders.length === 0}
                      <p class="text-muted-foreground">Keine Unterordner.</p>
                    {:else}
                      <ul class="max-h-48 overflow-auto">
                        {#each picker.folders as folder (folder)}
                          <li>
                            <button type="button" class="text-left underline" onclick={() => void openFolder(folder)}>
                              {folder.split('/').at(-1)}/
                            </button>
                          </li>
                        {/each}
                      </ul>
                    {/if}
                    {#if picker.truncated}<p class="text-muted-foreground">Nur die ersten 500 Ordner.</p>{/if}
                  </div>
                {/if}
              {/if}
              {#if current.printer_auto}
                <label class="flex items-center gap-2 text-sm @md:col-span-2">
                  <input type="checkbox" bind:checked={editing.enabled} disabled={Boolean(editing.skip_copy_check)} />
                  aktiv (automatisch alle {current.interval || '…'}, höchstens {current.printer_auto_max} Dateien je
                  Lauf{current.printer_auto_dry_run ? ', zurzeit nur Probelauf' : ''})
                </label>
              {/if}
              <p class="text-sm text-muted-foreground @md:col-span-2">
                Nie während eines Drucks, nie die Datei des laufenden oder letzten Drucks und nie Dateien
                ohne Datum. Dateien ohne Kopie zählen zu den neuesten, werden aber nie gelöscht. Von Hand
                wird mit deinen Rechten am Drucker gelöscht{current.printer_auto
                  ? ', automatisch als Erweiterung, solange du den Drucker selbst bedienen darfst'
                  : ''}.
              </p>
            {:else}
              <label class="flex flex-col gap-1 text-sm">
                Speicher
                <select class={field} bind:value={editing.storage}>
                  {#each storages.length ? storages : [editing.storage ?? ''] as name (name)}
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
            {/if}
            {#if editing.target === 'printer'}
              <!-- Druckdateien werden immer einzeln beurteilt. -->
            {:else if editing.mode === 'files'}
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
              {editing.target === 'printer' ? 'Behalten (Tage, 0 = nur nach Anzahl)' : 'Behalten (Tage)'}
              <input
                class={field}
                type="number"
                min={editing.target === 'printer' ? 0 : 1}
                max="3650"
                bind:value={editing.keep_days}
                required
              />
            </label>
            <label class="flex flex-col gap-1 text-sm">
              Die neuesten immer behalten (Anzahl)
              <input class={field} type="number" min="0" bind:value={editing.keep_newest} />
            </label>
            {#if editing.target !== 'printer'}
              <label class="flex items-center gap-2 text-sm">
                <input type="checkbox" bind:checked={editing.enabled} />
                aktiv (für den automatischen Lauf)
              </label>
            {/if}
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
      {#if current?.automatic || current?.printer_automatic}
        <p class="text-sm">
          Alle {current.interval} werden die aktiven Regeln ausgeführt, jede nur, solange die Person,
          die sie gespeichert hat, dort selbst löschen darf.
          {current.automatic
            ? 'Server-Speicher: an.'
            : 'Server-Speicher: aus, dafür fehlt das api_token.'}
          {current.printer_automatic
            ? `Drucker: an, höchstens ${current.printer_auto_max} Dateien je Regel und Lauf, nur mit Kopie in Connect Local${current.printer_auto_dry_run ? ', zurzeit nur als Probelauf (es wird nichts gelöscht, nur im Verlauf notiert)' : ''}.`
            : 'Drucker: nur von Hand.'}
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
              · {run.rule} ·
              {run.dry_run
                ? `${run.units} würden gelöscht · ${bytes(run.freed)} würden frei`
                : `${run.units} gelöscht · ${bytes(run.freed)} frei`}
              · {run.by}
              {#if run.dry_run}<Badge variant="secondary">Probelauf</Badge>{/if}
              {#if run.partial}<Badge variant="warning">unvollständig</Badge>{/if}
              {#if run.error}<span class="text-destructive-text"> · {run.error}</span>{/if}
              {#if run.deleted?.length}
                <details>
                  <summary class="cursor-pointer text-muted-foreground">
                    {run.dry_run ? 'Wäre gelöscht worden' : 'Gelöscht'}: {run.deleted.length} Dateien
                  </summary>
                  <ul class="mt-1 max-h-48 overflow-auto">
                    {#each run.deleted as file, j (j)}<li>{file}</li>{/each}
                  </ul>
                </details>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </CardContent>
  </Card>
</section>
