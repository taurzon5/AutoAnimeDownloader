<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import {
    getConfig,
    updatePriorities,
    getPriorityDefaults,
    getQueues,
    createQueue,
    renameQueue,
    orderQueues,
    deleteQueue,
    updateQueuePriorities,
    type Config,
    type Priorities,
    type Queue,
  } from "../lib/api/client.js";
  import ConfirmDialog from "../components/ConfirmDialog.svelte";
  import { DEFAULT_QUEUE_ID } from "../lib/domain/queues.js";
  import Loading from "../components/Loading.svelte";
  import Checkbox from "../components/ui/Checkbox.svelte";
  import { toast } from "../lib/stores/toast.js";
  import { PRESETS, applyPreset } from "../lib/domain/priorityPresets.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import { setUnsaved, snapshot } from "../lib/stores/unsavedChanges.js";

  // scope: source applies to movies and FINISHED series; audio stays movies-only.
  const LISTS: { key: keyof Priorities; label: () => string; scope?: () => string }[] = [
    { key: "criteria_order", label: m.priorities_list_criteria_order },
    { key: "fansubs", label: m.priorities_list_fansubs },
    { key: "resolutions", label: m.priorities_list_resolutions },
    { key: "sources", label: m.priorities_list_sources, scope: m.priorities_scope_movies_and_finished },
    { key: "codecs", label: m.priorities_list_codecs },
    { key: "audio", label: m.priorities_list_audio, scope: m.priorities_scope_movies_only },
    { key: "ignore_list", label: m.priorities_list_ignore_list },
  ];

  // A fixed line instead of a tooltip: tooltips don't exist on touch, and this screen is used on
  // the phone.
  const NOTES: Partial<Record<keyof Priorities, () => string>> = {
    criteria_order: m.priorities_note_criteria_order,
    codecs: m.priorities_note_codecs,
  };

  $: T = $locale && {
    title: m.priorities_title(),
    subtitle: m.priorities_subtitle(),
    loading: m.priorities_loading(),
    resetList: m.priorities_reset_list(),
    addPlaceholder: m.priorities_add_placeholder(),
    resetAll: m.priorities_reset_all(),
    save: m.priorities_save(),
    saving: m.priorities_saving(),
    lists: LISTS.map((l) => ({ ...l, label: l.label(), scope: l.scope && l.scope() })),
    notes: Object.fromEntries(Object.entries(NOTES).map(([k, f]) => [k, f()])) as Partial<Record<keyof Priorities, string>>,
    presets: Object.fromEntries(
      Object.entries(PRESETS).map(([k, list]) => [k, (list ?? []).map((p) => ({ ...p, label: p.label(), desc: p.desc() }))]),
    ) as Partial<Record<keyof Priorities, { key: string; label: string; desc: string; first: string[] }[]>>,
  };

  let config: Config | null = null;
  let defaults: Priorities | null = null;
  let loading = true;
  let saving = false;
  let newItem: Record<string, string> = {};

  // The download queues (decisions/164): each ranks with its own lists. The default queue's are
  // config.json's priorities, saved through PUT /config/priorities as before; the others through
  // their queue. Without a second queue the page is the one ranking it always was.
  let queues: Queue[] = [];
  let selected = DEFAULT_QUEUE_ID;
  let rankings: Record<string, Priorities> = {};
  // What was loaded (or last saved), per queue: dirty means "differs from it", so undoing a change
  // clears it.
  let baselines: Record<string, string> = {};
  let newQueueName = "";
  let renaming: string | null = null;
  let renameValue = "";
  let deleting: Queue | null = null;
  let deleteOpen = false;

  $: current = rankings[selected] ?? null;
  $: changed = Object.keys(rankings).filter((id) => snapshot(rankings[id]) !== baselines[id]);
  $: setUnsaved("priorities", changed.length > 0);
  $: lists = T ? T.lists.filter((l) => selected === DEFAULT_QUEUE_ID || l.key !== "ignore_list") : [];
  onDestroy(() => setUnsaved("priorities", false));

  const clone = (p: Priorities): Priorities => JSON.parse(JSON.stringify(p));

  async function load() {
    try {
      loading = true;
      const [c, d] = await Promise.all([getConfig(), getPriorityDefaults()]);
      config = c;
      defaults = d;
      rankings = { [DEFAULT_QUEUE_ID]: clone(c.priorities) };
      try {
        queues = (await getQueues()) ?? [];
      } catch {
        queues = [];
      }
      for (const q of queues) {
        if (!q.default) rankings[q.id] = clone(q.priorities);
      }
      baselines = Object.fromEntries(Object.entries(rankings).map(([id, p]) => [id, snapshot(p)]));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.priorities_toast_load_error());
    } finally {
      loading = false;
    }
  }

  /** Replaces one list of the selected queue's ranking. */
  function setList(key: keyof Priorities, list: string[]) {
    if (!current) return;
    rankings[selected] = { ...current, [key]: list };
    rankings = rankings;
  }

  function move(key: keyof Priorities, i: number, dir: -1 | 1) {
    if (!current) return;
    const list = [...current[key]];
    const j = i + dir;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    setList(key, list);
  }

  async function addQueue() {
    const name = newQueueName.trim();
    if (!name) return;
    try {
      const q = await createQueue(name);
      queues = [...queues, q];
      rankings[q.id] = clone(q.priorities);
      baselines[q.id] = snapshot(rankings[q.id]);
      rankings = rankings;
      newQueueName = "";
      selected = q.id;
      toast.success(m.queues_toast_created({ name: q.name }));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.queues_toast_error());
    }
  }

  async function moveQueue(i: number, dir: -1 | 1) {
    const j = i + dir;
    if (j < 0 || j >= queues.length) return;
    const next = [...queues];
    [next[i], next[j]] = [next[j], next[i]];
    try {
      await orderQueues(next.map((q) => q.id));
      queues = next;
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.queues_toast_error());
    }
  }

  function startRename(q: Queue) {
    renaming = q.id;
    renameValue = q.name;
  }

  async function saveRename(q: Queue) {
    const name = renameValue.trim();
    renaming = null;
    if (!name || name === q.name) return;
    try {
      await renameQueue(q.id, name);
      queues = queues.map((x) => (x.id === q.id ? { ...x, name } : x));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.queues_toast_error());
    }
  }

  async function confirmDelete() {
    const q = deleting;
    deleting = null;
    if (!q) return;
    try {
      const { moved_to_default } = await deleteQueue(q.id);
      queues = queues.filter((x) => x.id !== q.id);
      delete rankings[q.id];
      delete baselines[q.id];
      rankings = rankings;
      if (selected === q.id) selected = DEFAULT_QUEUE_ID;
      toast.success(m.queues_toast_deleted({ name: q.name, count: moved_to_default }));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.queues_toast_error());
    }
  }

  /**
   * The list's rows: what is saved (checked, in order) followed by the canonical tokens left out
   * (unchecked, at the end). A token missing from the list is already treated as the worst by the
   * backend (`priorityIndex` returns `len(list)`), so "unchecked at the end" is exactly what
   * unchecking means — and nothing disappears from the screen, which was the problem with the X.
   *
   * `custom` = not in the default: what the user typed, plus the inert leftovers of an old
   * config.json (`x265`, `4k`). Only those can be removed for good.
   */
  function rows(key: keyof Priorities, list: string[], def: Priorities | null) {
    const canon = def?.[key] ?? [];
    return [
      ...list.map((item) => ({ item, on: true, custom: !canon.includes(item) })),
      ...canon.filter((v) => !list.includes(v)).map((item) => ({ item, on: false, custom: false })),
    ];
  }

  function toggle(key: keyof Priorities, item: string, on: boolean) {
    if (!current) return;
    setList(key, on ? [...current[key], item] : current[key].filter((v) => v !== item));
  }

  function remove(key: keyof Priorities, i: number) {
    if (!current) return;
    setList(key, current[key].filter((_, idx) => idx !== i));
  }

  function add(key: keyof Priorities) {
    if (!current) return;
    const v = (newItem[key] ?? "").trim().toLowerCase();
    if (!v || current[key].includes(v)) return;
    setList(key, [...current[key], v]);
    newItem[key] = "";
  }

  function preset(key: keyof Priorities, first: string[]) {
    if (!current) return;
    setList(key, applyPreset(current[key], first));
  }

  function resetList(key: keyof Priorities) {
    if (!current || !defaults) return;
    setList(key, [...defaults[key]]);
  }

  function resetAll() {
    if (!current || !defaults) return;
    // A queue's ignore list is not its own: reset only touches what it ranks with.
    rankings[selected] = selected === DEFAULT_QUEUE_ID ? clone(defaults) : { ...clone(defaults), ignore_list: current.ignore_list };
    rankings = rankings;
  }

  async function save() {
    if (!current) return;
    try {
      saving = true;
      // Only the lists: a full save would also run the folder and client checks (an unplugged
      // drive elsewhere would refuse a reorder). Every queue with a change is saved.
      for (const id of changed) {
        if (id === DEFAULT_QUEUE_ID) await updatePriorities(rankings[id]);
        else await updateQueuePriorities(id, rankings[id]);
        baselines[id] = snapshot(rankings[id]);
      }
      baselines = baselines;
      toast.success(m.priorities_toast_saved());
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.priorities_toast_save_error());
    } finally {
      saving = false;
    }
  }

  onMount(load);
</script>

<div class="space-y-6">
  <div>
    <h1 class="text-2xl font-semibold text-heading">{T && T.title}</h1>
    <p class="text-sm text-subtle mt-0.5">{T && T.subtitle}</p>
  </div>

  {#if loading}
    <Loading message={(T && T.loading) || ""} />
  {:else if config && current && T}
    {#if queues.length > 0}
      <div class="flex flex-col gap-3 rounded-card border border-default bg-sunken p-5" data-testid="queues">
        <div>
          <h2 class="text-sm font-semibold text-subtle uppercase tracking-wider">{$locale && m.queues_title()}</h2>
          <p class="mt-1 text-xs text-subtle">{$locale && m.queues_hint()}</p>
        </div>
        <ol class="flex flex-col gap-1.5">
          {#each queues as q, i (q.id)}
            <li class="flex flex-wrap items-center gap-2 bg-card rounded-md px-3 py-1.5">
              <span class="text-xs text-subtle w-5 text-right">{i + 1}</span>
              {#if renaming === q.id}
                <input
                  type="text"
                  bind:value={renameValue}
                  aria-label={$locale && m.queues_rename_label({ name: q.name })}
                  class="flex-1 min-w-0 rounded-md border-default bg-control text-heading text-sm px-2 py-1"
                  on:keydown={(e) => { if (e.key === "Enter") { e.preventDefault(); saveRename(q); } if (e.key === "Escape") renaming = null; }}
                />
                <button type="button" class="text-xs font-medium text-accent" on:click={() => saveRename(q)}>{$locale && m.queues_rename_save()}</button>
              {:else}
                <span class="flex-1 min-w-0 truncate text-sm text-heading">{q.name}</span>
                <span class="text-xs text-subtle">
                  {$locale && (q.default ? m.queues_default_count() : m.queues_count({ count: q.animes.length }))}
                </span>
                <button type="button" class="text-xs font-medium text-subtle hover:text-heading" on:click={() => startRename(q)}>{$locale && m.queues_rename()}</button>
              {/if}
              <button
                type="button"
                on:click={() => moveQueue(i, -1)}
                disabled={i === 0}
                aria-label={$locale && m.queues_move_up({ name: q.name })}
                class="text-subtle hover:text-heading disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              >↑</button>
              <button
                type="button"
                on:click={() => moveQueue(i, 1)}
                disabled={i === queues.length - 1}
                aria-label={$locale && m.queues_move_down({ name: q.name })}
                class="text-subtle hover:text-heading disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              >↓</button>
              {#if !q.default}
                <button
                  type="button"
                  on:click={() => { deleting = q; deleteOpen = true; }}
                  aria-label={$locale && m.queues_delete({ name: q.name })}
                  class="text-subtle hover:text-danger transition-colors"
                >
                  <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
                  </svg>
                </button>
              {/if}
            </li>
          {/each}
        </ol>
        <form class="flex gap-2" on:submit|preventDefault={addQueue}>
          <input
            type="text"
            bind:value={newQueueName}
            placeholder={$locale && m.queues_new_placeholder()}
            aria-label={$locale && m.queues_new_placeholder()}
            class="flex-1 block rounded-md border-default bg-control text-heading shadow-sm focus:border-accent sm:text-sm px-3 py-2"
          />
          <button
            type="submit"
            disabled={!newQueueName.trim()}
            class="inline-flex items-center px-3 py-2 rounded-md border border-default text-body hover:bg-control text-sm font-medium transition-colors disabled:opacity-50"
          >{$locale && m.queues_add()}</button>
        </form>
        {#if queues.length > 1}
          <div class="flex flex-wrap items-center gap-2">
            <label for="ranking_queue" class="text-xs text-subtle">{$locale && m.queues_ranking_for()}</label>
            <select
              id="ranking_queue"
              bind:value={selected}
              class="rounded-md border border-default bg-control px-3 py-1.5 text-sm text-body"
            >
              {#each queues as q (q.id)}
                <option value={q.id}>{q.name}</option>
              {/each}
            </select>
          </div>
        {/if}
      </div>
    {/if}

    {#if selected !== DEFAULT_QUEUE_ID}
      <p class="text-xs text-subtle">{$locale && m.queues_ignore_shared()}</p>
    {/if}

    <div class="space-y-4">
      {#each lists as { key, label, scope } (key)}
        {@const items = rows(key, current[key], defaults)}
        <div class="flex flex-col rounded-card border border-default bg-sunken">
          <div class="flex flex-col gap-3 p-5">
            <div class="flex items-center justify-between">
              <h2 class="text-sm font-semibold text-subtle uppercase tracking-wider">
                {label}
                {#if scope}<span class="normal-case tracking-normal font-normal text-subtle">({scope})</span>{/if}
              </h2>
              <button
                type="button"
                on:click={() => resetList(key)}
                class="text-xs font-medium text-subtle hover:text-heading transition-colors"
              >
                {T.resetList}
              </button>
            </div>

            {#if T.presets[key]}
              <div class="flex flex-wrap gap-2">
                {#each T.presets[key] ?? [] as p (p.key)}
                  <button
                    type="button"
                    on:click={() => preset(key, p.first)}
                    title={p.desc}
                    class="px-3 py-1.5 rounded-md border border-default text-body hover:bg-control text-xs font-medium transition-colors"
                  >
                    {p.label}
                  </button>
                {/each}
              </div>
            {/if}

            {#if items.length > 0}
              <ol class="flex flex-col gap-1.5">
                {#each items as row, i (row.item)}
                  <li class="flex items-center gap-2 bg-card rounded-md px-3 py-1.5 {row.on ? '' : 'opacity-50'}">
                    <Checkbox
                      checked={row.on}
                      disabled={row.custom}
                      label={$locale && (row.custom ? m.priorities_item_custom({ item: row.item }) : m.priorities_item_use({ item: row.item }))}
                      labelHidden
                      on:change={() => toggle(key, row.item, !row.on)}
                    />
                    <span class="text-xs text-subtle w-5 text-right">{row.on ? i + 1 : ""}</span>
                    <span class="flex-1 text-sm text-heading">{row.item}</span>
                    {#if row.on}
                      <button
                        type="button"
                        on:click={() => move(key, i, -1)}
                        disabled={i === 0}
                        aria-label={$locale && m.priorities_move_up({ item: row.item })}
                        class="text-subtle hover:text-heading disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                      >
                        ↑
                      </button>
                      <button
                        type="button"
                        on:click={() => move(key, i, 1)}
                        disabled={i === current[key].length - 1}
                        aria-label={$locale && m.priorities_move_down({ item: row.item })}
                        class="text-subtle hover:text-heading disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                      >
                        ↓
                      </button>
                    {/if}
                    {#if row.custom}
                      <button
                        type="button"
                        on:click={() => remove(key, i)}
                        aria-label={$locale && m.priorities_remove({ item: row.item })}
                        class="text-subtle hover:text-danger transition-colors"
                      >
                        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
                        </svg>
                      </button>
                    {/if}
                  </li>
                {/each}
              </ol>
            {/if}

            {#if T.notes[key]}
              <p class="text-xs text-subtle">{T.notes[key]}</p>
            {/if}

            <!-- criteria_order is a closed set (sortByCriteria silently skips a criterion it doesn't
                 know) and every criterion already shows above, checked or not: there's nothing to
                 add, and free text there would only produce inert config. -->
            {#if key !== "criteria_order"}
            <div class="flex gap-2">
              <input
                type="text"
                bind:value={newItem[key]}
                placeholder={T.addPlaceholder}
                aria-label={T.addPlaceholder}
                class="flex-1 block rounded-md border-default bg-control text-heading shadow-sm focus:border-accent sm:text-sm px-3 py-2"
                on:keydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add(key); } }}
              />
              <button
                type="button"
                on:click={() => add(key)}
                aria-label={T.addPlaceholder}
                class="inline-flex items-center px-3 py-2 rounded-md border border-default text-body hover:bg-control text-sm font-medium transition-colors"
              >
                +
              </button>
            </div>
            {/if}
          </div>
        </div>
      {/each}
    </div>

    <ConfirmDialog
      bind:open={deleteOpen}
      title={$locale && deleting ? m.queues_delete_title({ name: deleting.name }) : ""}
      message={$locale && m.queues_delete_msg()}
      confirmLabel={$locale && m.queues_delete_confirm()}
      cancelLabel={$locale && m.common_cancel()}
      on:confirm={confirmDelete}
    />

    <div class="flex justify-end gap-3 pt-2">
      <button
        type="button"
        on:click={resetAll}
        disabled={saving}
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-default text-body hover:bg-control font-medium text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {T.resetAll}
      </button>
      <button
        type="button"
        on:click={save}
        disabled={saving}
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-on-accent hover:opacity-90 font-medium text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {saving ? T.saving : T.save}
      </button>
    </div>
  {/if}
</div>
