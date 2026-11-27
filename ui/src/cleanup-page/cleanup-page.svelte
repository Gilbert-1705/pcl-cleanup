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
    CallError,
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
  const t = host.t;

  let current = $state<State | undefined>(undefined);
  let plans = $state<Record<string, RulePlan>>({});
  let scanning = $state(false);
  let problem = $state('');
  let notice = $state('');
  // The rule being cleaned, '*' for the printer run, 'due' for the due rules.
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

  // Folder picker for the copies of a printer rule.
  let picker = $state<Folders | undefined>(undefined);
  let pickerBusy = $state(false);

  const load = async (): Promise<void> => {
    try {
      current = await readState(host);
      problem = current.error ?? '';
    } catch (error) {
      problem = t('error.state', { detail: String(error) });
    }
  };

  const scan = async (): Promise<void> => {
    scanning = true;
    try {
      const list = await readPlans(host);
      plans = Object.fromEntries(list.map((plan) => [plan.rule_id, plan]));
    } catch (error) {
      problem = t('error.scan', { detail: String(error) });
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
      problem = t('error.usage', { detail: String(error) });
    } finally {
      usageBusy = false;
    }
  };

  // Only what the extension's settings allow (allowed_storages,
  // allowed_devices), and of the printers only those the signed-in person may
  // operate: deleting there happens with their rights.
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

  // Active storage rules whose plan, made with the reader's rights, has
  // something to delete. Printer rules are read one by one and have their own
  // automatic run, so they are not part of this.
  const dueRules = $derived(
    (current?.rules ?? []).filter((rule) => {
      if (rule.target === 'printer' || !rule.enabled) return false;
      const plan = plans[rule.id];

      return plan !== undefined && !plan.error && !plan.lazy && plan.delete_units > 0;
    }),
  );
  const dueSize = $derived(dueRules.reduce((sum, rule) => sum + (plans[rule.id]?.delete_size ?? 0), 0));

  // Reading the printer takes a while and occupies it, so only on request.
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

  const unitsOf = (rule: Rule, count: number): string =>
    t(rule.target !== 'printer' && rule.mode === 'folders' ? 'units.folders' : 'units.files', { count });

  // Plans one rule again, deletes what the plan names with the reader's own
  // rights and writes the run to the history. Answers the space freed and a
  // line for the reader.
  const deleteFor = async (rule: Rule): Promise<{ freed: number; text: string }> => {
    progress = t('progress.searching');

    const plan = await readPlan(host, rule.id);
    const paths = plan?.delete ?? [];
    if (!plan || paths.length === 0) return { freed: 0, text: t('notice.nothing', { rule: rule.name }) };

    let failure = '';
    let done = 0;
    const step = (n: number): void => {
      progress = t('progress.deleted', { rule: rule.name, done: n, total: paths.length });
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

    const text = failure
      ? t('notice.failed', { rule: rule.name, count: done, detail: failure })
      : t(record.partial ? 'notice.doneMore' : 'notice.done', { rule: rule.name, count: done, size: bytes(record.freed) });

    return { freed: record.freed, text };
  };

  const finish = async (): Promise<void> => {
    busyRule = '';
    progress = '';
    await load();
    await scan();
    await loadUsage(true);
  };

  const clean = async (rule: Rule): Promise<void> => {
    busyRule = rule.id;
    notice = '';
    problem = '';

    try {
      notice = (await deleteFor(rule)).text;
    } catch (error) {
      problem = String(error);
    } finally {
      await finish();
    }
  };

  // One rule after the other, each planned again just before it deletes.
  const runDue = async (): Promise<void> => {
    const rules = [...dueRules];
    busyRule = 'due';
    notice = '';
    problem = '';

    let freed = 0;
    const lines: string[] = [];
    try {
      for (const rule of rules) {
        const result = await deleteFor(rule);
        freed += result.freed;
        lines.push(result.text);
      }
    } catch (error) {
      problem = String(error);
    } finally {
      notice = [t('notice.dueDone', { count: lines.length, size: bytes(freed) }), ...lines].join(' ');
      await finish();
    }
  };

  const runAutomatic = async (): Promise<void> => {
    busyRule = '*';
    try {
      const answer = await runNow(host);
      notice = t('notice.printerRun', { count: answer.runs.length });
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
      problem = t('error.folders', { detail: String(error) });
    } finally {
      pickerBusy = false;
    }
  };

  const parentOf = (path: string): string => path.split('/').slice(0, -1).join('/');

  const takeFolder = (path: string): void => {
    if (editing) editing = { ...editing, copy_path: path };
    picker = undefined;
  };

  // Turns the copy check on or off. Off means: no copy storage at all.
  const chooseCopyCheck = (on: boolean): void => {
    if (!editing) return;
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { copy_storage, copy_path, skip_copy_check, ...rest } = editing;
    // Without the copy check, a printer rule never runs on its own.
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
            id: `rule-${Date.now().toString(36)}`,
            name: t('rule.newName'),
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

  // Switching to a printer's storage sets what a printer rule needs: print
  // files only, one by one, by hand, only with a copy on the server. Default:
  // keep the newest 10, no age limit.
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

  // Saves the list made on the version the overview gave. When somebody else
  // saved in between, the server refuses, and the page loads the new list.
  const saveRules = async (rules: Rule[]): Promise<boolean> => {
    try {
      await writeRules(host, rules, current?.rules_version ?? '');
      await load();
      await scan();

      return true;
    } catch (error) {
      if (error instanceof CallError && error.status === 412) {
        problem = t('error.stale');
        await load();
        await scan();
      } else {
        problem = t('error.save', { detail: String(error) });
      }

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
    // The server sets saved_by, never the page. A folder rule judges whole
    // folders and takes no pattern.
    delete rule.saved_by;
    if (rule.target !== 'printer' && rule.mode === 'folders') delete rule.pattern;
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
    const days = Number(rule.keep_days);
    const newest = rule.keep_newest ?? 0;
    if (days === 0) return t('rule.keep.newestOnly', { count: newest });

    return newest ? t('rule.keep.olderNewest', { days, count: newest }) : t('rule.keep.older', { days });
  };

  const describeCopy = (rule: Rule): string =>
    rule.skip_copy_check
      ? t('rule.copy.skip')
      : t('rule.copy.check', {
          storage: rule.copy_storage ?? '?',
          path: rule.copy_path || t('rule.copy.wholeStorage'),
        });

  const describeDelete = (rule: Rule): string =>
    rule.target === 'printer'
      ? t('rule.deleteCopy', { keep: describeKeep(rule), copy: describeCopy(rule) })
      : t('rule.delete', { keep: describeKeep(rule) });

  const describeWhat = (rule: Rule): string => {
    if (rule.target === 'printer') return t('rule.what.printer', { path: rule.path, pattern: rule.pattern ?? '' });
    if (rule.mode === 'folders') return t('rule.what.folders', { path: rule.path, depth: rule.depth ?? 1 });

    return rule.pattern
      ? t('rule.what.filesPattern', { path: rule.path, pattern: rule.pattern })
      : t('rule.what.files', { path: rule.path });
  };

  const describeWhere = (rule: Rule): string =>
    rule.target === 'printer' ? t('rule.where.printer', { name: printerName(rule.device) }) : (rule.storage ?? '');

  const confirmText = (rule: Rule, plan: RulePlan | undefined): string => {
    const units = plan ? unitsOf(rule, plan.delete_units) : t('units.matching');
    const place = `${describeWhere(rule)}:${rule.path}`;
    const keep = describeKeep(rule);
    const main = plan
      ? t('rule.cleanTextSize', { units, place, keep, size: bytes(plan.delete_size) })
      : t('rule.cleanText', { units, place, keep });
    if (rule.target !== 'printer') return main;

    return `${main} ${t(rule.skip_copy_check ? 'rule.cleanPrinter' : 'rule.cleanPrinterCopies')}`;
  };

  const describeBy = (by: string): string => {
    if (by === 'schedule') return t('history.by.schedule');
    if (by === 'request') return t('history.by.request');
    if (by.startsWith('hand:')) return t('history.by.hand', { person: by.slice('hand:'.length) });
    // By hand, by someone the reader may not learn about.
    if (by === 'hand') return t('history.by.handHidden');

    // A run written by a version before 0.5.0.
    return by;
  };

  const field =
    'h-8 rounded-sm border border-border bg-background px-2 text-sm text-foreground';

  const share = (size: number): string =>
    usage && usage.size > 0 ? `${Math.max(1, Math.round((size / usage.size) * 100))}%` : '0%';
</script>

<section class="flex flex-col gap-4">
  <header class="flex flex-wrap items-center justify-between gap-3">
    <h1 class="text-lg font-semibold">{t('page.title')}</h1>
    <div class="flex items-center gap-2">
      <Button variant="secondary" size="sm" disabled={scanning} onclick={() => void scan()}>
        {scanning ? t('page.scanning') : t('page.rescan')}
      </Button>
    </div>
  </header>

  <p class="text-sm text-muted-foreground">{t('page.intro')}</p>

  {#if problem}<p aria-live="polite" class="text-sm text-destructive-text">{problem}</p>{/if}
  {#if notice}<p aria-live="polite" class="text-sm">{notice}</p>{/if}
  {#if progress}<p class="text-sm text-muted-foreground">{progress}</p>{/if}

  <Card>
    <CardHeader>
      <CardTitle>{t('due.title')}</CardTitle>
    </CardHeader>
    <CardContent>
      {#if !current || (scanning && Object.keys(plans).length === 0)}
        <p class="text-sm text-muted-foreground">{t('page.scanning')}</p>
      {:else if dueRules.length === 0}
        <p class="text-sm text-muted-foreground">{t('due.none')}</p>
      {:else}
        <p class="text-sm">{t('due.summary', { count: dueRules.length, size: bytes(dueSize) })}</p>
        <ul class="mt-1 text-sm text-muted-foreground">
          {#each dueRules as rule (rule.id)}
            {@const plan = plans[rule.id]}
            <li>
              {t('due.item', {
                rule: rule.name,
                units: unitsOf(rule, plan?.delete_units ?? 0),
                size: bytes(plan?.delete_size ?? 0),
              })}
            </li>
          {/each}
        </ul>
        <div class="mt-2">
          <ConfirmDialog
            title={t('due.confirmTitle')}
            description={t('due.confirmText', { count: dueRules.length, size: bytes(dueSize) })}
            confirmLabel={t('rule.confirmDelete')}
            variant="destructive"
            onConfirm={() => void runDue()}
          >
            {#snippet trigger({ props })}
              <Button
                {...props}
                variant="destructive"
                size="sm"
                disabled={busyRule !== '' || scanning}
                pending={busyRule === 'due'}
              >
                {t('due.run')}
              </Button>
            {/snippet}
          </ConfirmDialog>
        </div>
      {/if}
      <p class="mt-3 text-xs text-muted-foreground">{t('due.later')}</p>
    </CardContent>
  </Card>

  <Card>
    <CardHeader>
      <CardTitle>{t('rules.title')}</CardTitle>
    </CardHeader>
    <CardContent>
      {#if !current}
        <p class="text-sm text-muted-foreground">{t('page.loading')}</p>
      {:else}
        <ul class="flex flex-col gap-3">
          {#each current.rules as rule, index (rule.id)}
            {@const plan = plans[rule.id]}
            {#if rule.hidden}
              <li class="flex flex-col gap-1 border-b border-border pb-3">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{rule.name}</span>
                  <Badge variant="secondary">{t('rule.badge.noAccess')}</Badge>
                </div>
                <p class="text-sm text-muted-foreground">{t('rule.hiddenText')}</p>
              </li>
            {:else}
              <li class="flex flex-col gap-2 border-b border-border pb-3">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{rule.name}</span>
                  {#if rule.target === 'printer' && !rule.enabled}
                    <Badge variant="secondary">{t('rule.badge.manualOnly')}</Badge>
                  {:else if rule.target === 'printer'}
                    <Badge variant="success">{t('rule.badge.active')}</Badge>
                    {#if current.printer_auto_dry_run}<Badge variant="warning">{t('rule.badge.dryRun')}</Badge>{/if}
                  {:else}
                    <Badge variant={rule.enabled ? 'success' : 'secondary'}>
                      {rule.enabled ? t('rule.badge.active') : t('rule.badge.off')}
                    </Badge>
                  {/if}
                  <span class="text-xs text-muted-foreground">
                    {t('rule.place', { where: describeWhere(rule), what: describeWhat(rule) })}
                  </span>
                </div>
                <p class="text-sm text-muted-foreground">{describeDelete(rule)}</p>
                {#if rule.target === 'printer' && rule.enabled && !current.printer_auto}
                  <p class="text-sm text-warning-text">{t('rule.printerAutoOff')}</p>
                {/if}
                {#if current.skipped?.[rule.id]}
                  <p class="text-sm text-warning-text">{t('rule.skipped', { reason: current.skipped[rule.id] ?? '' })}</p>
                {/if}

                {#if plan?.error}
                  <p class="text-sm text-destructive-text">{plan.error}</p>
                {:else if plan?.lazy}
                  <p class="text-sm text-muted-foreground">{t('plan.notRead')}</p>
                {:else if plan}
                  <p class="text-sm">
                    {t('plan.size', { units: unitsOf(rule, plan.units), size: bytes(plan.size) })}
                    {#if plan.oldest && when(plan.oldest)}· {t('plan.oldest', { date: when(plan.oldest) })}{/if}
                    · <strong>{t('plan.toDelete', { count: plan.delete_units, size: bytes(plan.delete_size) })}</strong>
                    {#if plan.truncated}· {t('plan.truncated')}{/if}
                  </p>
                  {#if rule.target === 'printer'}
                    <ul class="text-sm text-muted-foreground">
                      <li>{t('plan.keptNewest', { count: Math.min(rule.keep_newest ?? 0, plan.units) })}</li>
                      {#if plan.copy_check}
                        <li>
                          {plan.kept_no_copy
                            ? t('plan.keptNoCopySize', { count: plan.kept_no_copy, size: bytes(plan.kept_no_copy_size ?? 0) })
                            : t('plan.keptNoCopy', { count: 0 })}
                        </li>
                      {/if}
                      <li>{t('plan.deleteCount', { count: plan.delete_units })}</li>
                    </ul>
                    {#if plan.kept_no_copy_list?.length}
                      <details class="text-sm">
                        <summary class="cursor-pointer text-muted-foreground">{t('plan.noCopyList')}</summary>
                        <ul class="mt-1 max-h-48 overflow-auto">
                          {#each plan.kept_no_copy_list as name (name)}<li>{name}</li>{/each}
                        </ul>
                      </details>
                    {/if}
                  {/if}
                {:else if scanning}
                  <p class="text-sm text-muted-foreground">{t('page.scanning')}</p>
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
                      {t('rule.readPrinter')}
                    </Button>
                  {/if}
                  <ConfirmDialog
                    title={t('rule.cleanTitle', { rule: rule.name })}
                    description={confirmText(rule, plan)}
                    confirmLabel={t('rule.confirmDelete')}
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
                        {t('rule.cleanNow')}
                      </Button>
                    {/snippet}
                  </ConfirmDialog>
                  {#if rule.target !== 'printer' || rule.enabled || (current.printer_auto && !rule.skip_copy_check)}
                    <Button variant="secondary" size="sm" onclick={() => void toggle(index)}>
                      {rule.enabled ? t('rule.turnOff') : t('rule.turnOn')}
                    </Button>
                  {/if}
                  <Button variant="ghost" size="sm" onclick={() => startEdit(index)}>{t('rule.edit')}</Button>
                  <ConfirmDialog
                    title={t('rule.removeTitle')}
                    description={t('rule.removeText', { rule: rule.name })}
                    confirmLabel={t('rule.remove')}
                    variant="destructive"
                    onConfirm={() => void removeRule(index)}
                  >
                    {#snippet trigger({ props })}
                      <Button {...props} variant="ghost" size="sm">{t('rule.remove')}</Button>
                    {/snippet}
                  </ConfirmDialog>
                </div>
              </li>
            {/if}
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
              {t('form.name')}
              <input class={field} bind:value={editing.name} required maxlength="80" />
            </label>
            {#if printers.length > 0 || editing.target === 'printer'}
              <label class="flex flex-col gap-1 text-sm">
                {t('form.where')}
                <select
                  class={field}
                  value={editing.target ?? 'storage'}
                  onchange={(event) => chooseTarget(event.currentTarget.value as 'storage' | 'printer')}
                >
                  <option value="storage">{t('form.where.storage')}</option>
                  <option value="printer">{t('form.where.printer')}</option>
                </select>
              </label>
            {/if}
            {#if editing.target === 'printer'}
              <label class="flex flex-col gap-1 text-sm">
                {t('form.printer')}
                <select class={field} bind:value={editing.device} required>
                  {#each printers.length ? printers : [{ id: editing.device ?? '', name: editing.device ?? '' }] as printer (printer.id)}
                    <option value={printer.id}>{printer.name}</option>
                  {/each}
                </select>
              </label>
              <label class="flex flex-col gap-1 text-sm">
                {t('form.printerFolder')}
                <input class={field} bind:value={editing.path} required placeholder="usb" />
              </label>
              <label class="flex flex-col gap-1 text-sm">
                {t('form.printerPattern')}
                <input class={field} bind:value={editing.pattern} required placeholder="*.bgcode" />
              </label>
              <label class="flex items-center gap-2 text-sm @md:col-span-2">
                <input
                  type="checkbox"
                  checked={!editing.skip_copy_check}
                  onchange={(event) => chooseCopyCheck(event.currentTarget.checked)}
                />
                {t('form.copyCheck')}
              </label>
              {#if !editing.skip_copy_check}
                <label class="flex flex-col gap-1 text-sm">
                  {t('form.copyStorage')}
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
                  <span>{t('form.copyFolder')}</span>
                  <div class="flex flex-wrap items-center gap-2">
                    <code class="rounded-sm bg-muted px-2 py-1">{editing.copy_path || t('rule.copy.wholeStorage')}</code>
                    <Button
                      variant="secondary"
                      size="sm"
                      pending={pickerBusy}
                      onclick={() => void openFolder(editing?.copy_path ?? '')}
                    >
                      {t('form.pickFolder')}
                    </Button>
                  </div>
                </div>
                {#if picker}
                  <div class="flex flex-col gap-1 rounded-sm border border-border p-2 text-sm @md:col-span-2">
                    <div class="flex flex-wrap items-center gap-2">
                      <span class="text-muted-foreground">{picker.storage}:</span>
                      <code>{picker.path || '/'}</code>
                      <Button size="sm" onclick={() => takeFolder(picker?.path ?? '')}>{t('picker.take')}</Button>
                      {#if picker.path}
                        <Button variant="ghost" size="sm" onclick={() => void openFolder(parentOf(picker?.path ?? ''))}>
                          {t('picker.up')}
                        </Button>
                      {/if}
                      <Button variant="ghost" size="sm" onclick={() => (picker = undefined)}>{t('picker.close')}</Button>
                    </div>
                    {#if picker.folders.length === 0}
                      <p class="text-muted-foreground">{t('picker.empty')}</p>
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
                    {#if picker.truncated}<p class="text-muted-foreground">{t('picker.truncated')}</p>{/if}
                  </div>
                {/if}
              {/if}
              {#if current.printer_auto}
                <label class="flex items-center gap-2 text-sm @md:col-span-2">
                  <input type="checkbox" bind:checked={editing.enabled} disabled={Boolean(editing.skip_copy_check)} />
                  {t(current.printer_auto_dry_run ? 'form.printerActiveDry' : 'form.printerActive', {
                    interval: current.interval || '…',
                    max: current.printer_auto_max,
                  })}
                </label>
              {/if}
              <p class="text-sm text-muted-foreground @md:col-span-2">
                {t(current.printer_auto ? 'form.printerNoteAuto' : 'form.printerNote')}
              </p>
            {:else}
              <label class="flex flex-col gap-1 text-sm">
                {t('form.storage')}
                <select class={field} bind:value={editing.storage}>
                  {#each storages.length ? storages : [editing.storage ?? ''] as name (name)}
                    <option value={name}>{name}</option>
                  {/each}
                </select>
              </label>
              <label class="flex flex-col gap-1 text-sm">
                {t('form.folder')}
                <input class={field} bind:value={editing.path} required placeholder={t('form.folderPlaceholder')} />
              </label>
              <label class="flex flex-col gap-1 text-sm">
                {t('form.mode')}
                <select class={field} bind:value={editing.mode}>
                  <option value="files">{t('form.mode.files')}</option>
                  <option value="folders">{t('form.mode.folders')}</option>
                </select>
              </label>
            {/if}
            {#if editing.target === 'printer'}
              <!-- Print files are always judged one by one. -->
            {:else if editing.mode === 'files'}
              <label class="flex flex-col gap-1 text-sm">
                {t('form.pattern')}
                <input class={field} bind:value={editing.pattern} placeholder="*.jpg" />
              </label>
            {:else}
              <label class="flex flex-col gap-1 text-sm">
                {t('form.depth')}
                <input class={field} type="number" min="1" max="5" bind:value={editing.depth} />
              </label>
            {/if}
            <label class="flex flex-col gap-1 text-sm">
              {editing.target === 'printer' ? t('form.keepDaysPrinter') : t('form.keepDays')}
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
              {t('form.keepNewest')}
              <input class={field} type="number" min="0" bind:value={editing.keep_newest} />
            </label>
            {#if editing.target !== 'printer'}
              <label class="flex items-center gap-2 text-sm">
                <input type="checkbox" bind:checked={editing.enabled} />
                {t('form.active')}
              </label>
            {/if}
            <div class="flex gap-2 @md:col-span-2">
              <Button type="submit" size="sm">{t('form.save')}</Button>
              <Button variant="ghost" size="sm" onclick={() => (editing = undefined)}>{t('form.cancel')}</Button>
            </div>
          </form>
        {:else}
          <div class="mt-3">
            <Button variant="secondary" size="sm" onclick={() => startEdit(-1)}>{t('rule.add')}</Button>
          </div>
        {/if}
      {/if}
    </CardContent>
  </Card>

  <Card>
    <CardHeader>
      <CardTitle>{t('auto.title')}</CardTitle>
    </CardHeader>
    <CardContent>
      {#if current?.printer_automatic}
        <p class="text-sm">
          {t(current.printer_auto_dry_run ? 'auto.onDry' : 'auto.on', {
            interval: current.interval ?? '',
            max: current.printer_auto_max,
          })}
          {#if when(current.next_run)}{t('auto.next', { date: when(current.next_run) })}{/if}
        </p>
        <div class="mt-2">
          <Button
            variant="secondary"
            size="sm"
            disabled={busyRule !== ''}
            pending={busyRule === '*'}
            onclick={() => void runAutomatic()}>{t('auto.runNow')}</Button
          >
        </div>
      {:else}
        <p class="text-sm text-muted-foreground">{t('auto.off')}</p>
      {/if}
    </CardContent>
  </Card>

  <Card>
    <CardHeader>
      <CardTitle>{t('usage.title')}</CardTitle>
    </CardHeader>
    <CardContent>
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <select
          class={field}
          bind:value={usageStorage}
          onchange={() => void loadUsage(false)}
          aria-label={t('usage.storage')}
        >
          {#each storages as name (name)}
            <option value={name}>{name}</option>
          {/each}
        </select>
        <Button variant="ghost" size="sm" disabled={usageBusy} onclick={() => void loadUsage(true)}>
          {usageBusy ? t('usage.counting') : t('usage.recount')}
        </Button>
        {#if usage}
          <span class="text-sm text-muted-foreground">
            {t('usage.summary', { size: bytes(usage.size), count: usage.files, date: when(usage.scanned) })}
          </span>
        {/if}
      </div>
      {#if usage}
        <ul class="flex flex-col gap-2">
          {#each usage.items as item (item.path)}
            <li class="flex flex-col gap-1">
              <div class="flex justify-between gap-2 text-sm">
                <span>{item.dir ? `${item.path}/` : item.path}</span>
                <span class="text-muted-foreground">{t('usage.item', { size: bytes(item.size), count: item.files })}</span>
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
      <CardTitle>{t('history.title')}</CardTitle>
    </CardHeader>
    <CardContent>
      {#if !current || current.history.length === 0}
        <p class="text-sm text-muted-foreground">{t('history.empty')}</p>
      {:else}
        <ul class="flex flex-col gap-1">
          {#each current.history as run, i (i)}
            <li class="text-sm">
              <span class="text-muted-foreground">{when(run.at)}</span>
              · {run.rule === '*' ? t('history.allRules') : run.rule} ·
              {t(run.dry_run ? 'history.wouldDelete' : 'history.deleted', { count: run.units, size: bytes(run.freed) })}
              · {describeBy(run.by)}
              {#if run.dry_run}<Badge variant="secondary">{t('history.dryRun')}</Badge>{/if}
              {#if run.partial}<Badge variant="warning">{t('history.partial')}</Badge>{/if}
              {#if run.error}
                <span class="text-destructive-text">
                  · {run.error.startsWith('hidden:') ? t('history.hidden') : run.error}
                </span>
              {/if}
              {#if run.deleted?.length}
                <details>
                  <summary class="cursor-pointer text-muted-foreground">
                    {t(run.dry_run ? 'history.listWould' : 'history.listDeleted', { count: run.deleted.length })}
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
