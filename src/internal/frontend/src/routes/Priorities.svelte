<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
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
  import PriorityListsEditor from "../components/PriorityListsEditor.svelte";
  import { DEFAULT_QUEUE_ID } from "../lib/domain/queues.js";
  import Loading from "../components/Loading.svelte";
  import Checkbox from "../components/ui/Checkbox.svelte";
  import { toast } from "../lib/stores/toast.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import { setUnsaved, snapshot } from "../lib/stores/unsavedChanges.js";

  $: T = $locale && {
    title: m.priorities_title(),
    subtitle: m.priorities_subtitle(),
    loading: m.priorities_loading(),
    resetAll: m.priorities_reset_all(),
    save: m.priorities_save(),
    saving: m.priorities_saving(),
  };

  let config: Config | null = null;
  let defaults: Priorities | null = null;
  let loading = true;
  let saving = false;

  // The download queues (decisions/164): each ranks with its own criteria. The default queue's are
  // config.json's priorities, saved through PUT /config/priorities as before; the others through
  // their queue. The page lists the queues first and opens one queue's criteria on a click; with
  // a single queue (or none readable) its criteria are open from the start.
  let queues: Queue[] = [];
  let selected: string | null = DEFAULT_QUEUE_ID;
  let rankings: Record<string, Priorities> = {};
  // What was loaded (or last saved), per queue: dirty means "differs from it", so undoing a change
  // clears it.
  let baselines: Record<string, string> = {};
  let newQueueName = "";
  let renaming: string | null = null;
  let renameValue = "";
  let deleting: Queue | null = null;
  let deleteOpen = false;

  $: current = selected ? (rankings[selected] ?? null) : null;
  $: currentQueue = queues.find((q) => q.id === selected) ?? null;
  $: changed = Object.keys(rankings).filter((id) => snapshot(rankings[id]) !== baselines[id]);
  // The draft of "Same criteria for several queues" counts too: like an open form, edits not
  // applied yet are lost on leaving (decisions/129).
  $: draftDirty = draft !== null && snapshot(draft) !== draftBaseline;
  $: setUnsaved("priorities", changed.length > 0 || draftDirty);
  $: several = queues.length > 1;
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
      selected = queues.length > 1 ? null : DEFAULT_QUEUE_ID;
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.priorities_toast_load_error());
    } finally {
      loading = false;
    }
  }

  function setRanking(id: string, next: Priorities) {
    rankings[id] = next;
    rankings = rankings;
  }

  /** Opens a queue's criteria, or closes them when they are the ones open. */
  async function openQueue(id: string) {
    if (selected === id) {
      if (several) selected = null;
      return;
    }
    selected = id;
    await tick();
    document.getElementById("queue-criteria")?.scrollIntoView?.({ block: "start", behavior: "smooth" });
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
      delete targets[q.id];
      rankings = rankings;
      if (sameFrom === q.id) startFrom(DEFAULT_QUEUE_ID);
      if (selected === q.id || queues.length <= 1) selected = queues.length > 1 ? null : DEFAULT_QUEUE_ID;
      toast.success(m.queues_toast_deleted({ name: q.name, count: moved_to_default }));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.queues_toast_error());
    }
  }

  // Advanced: the same criteria for several queues. A draft starts from one queue's criteria
  // (unsaved edits included), is edited like any queue's, and is copied onto the queues ticked;
  // each of them can then be changed on its own. The ignore list is not part of it: it is the
  // default queue's alone and every queue shares it (decisions/164).
  let sameOpen = false;
  let sameFrom = DEFAULT_QUEUE_ID;
  let draft: Priorities | null = null;
  let draftBaseline = "";
  let targets: Record<string, boolean> = {};
  $: ticked = queues.filter((q) => targets[q.id]).map((q) => q.id);

  function startFrom(id: string) {
    sameFrom = id;
    draft = rankings[id] ? clone(rankings[id]) : null;
    draftBaseline = draft ? snapshot(draft) : "";
  }

  function onSameToggle(e: Event) {
    sameOpen = (e.currentTarget as HTMLDetailsElement).open;
    if (sameOpen && !draft) {
      startFrom(sameFrom);
      targets = Object.fromEntries(queues.map((q) => [q.id, true]));
    }
  }

  function tickAll(on: boolean) {
    targets = Object.fromEntries(queues.map((q) => [q.id, on]));
  }

  function applySame() {
    if (!draft || ticked.length === 0) return;
    for (const id of ticked) {
      rankings[id] = { ...clone(draft), ignore_list: rankings[id].ignore_list };
    }
    rankings = rankings;
    // Applied: from here the queues it went to carry the change, and they count as unsaved.
    draftBaseline = snapshot(draft);
    toast.info(m.queues_same_applied({ count: ticked.length }));
  }

  function resetAll() {
    if (!selected || !current || !defaults) return;
    // A queue's ignore list is not its own: reset only touches what it ranks with.
    setRanking(selected, selected === DEFAULT_QUEUE_ID ? clone(defaults) : { ...clone(defaults), ignore_list: current.ignore_list });
  }

  async function save() {
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
  {:else if config && rankings[DEFAULT_QUEUE_ID] && T}
    {#if queues.length > 0}
      <div class="flex flex-col gap-3 rounded-card border border-default bg-sunken p-5" data-testid="queues">
        <div>
          <h2 class="text-sm font-semibold text-subtle uppercase tracking-wider">{$locale && m.queues_title()}</h2>
          <p class="mt-1 text-xs text-subtle">{$locale && m.queues_hint()}</p>
        </div>
        <ol class="flex flex-col gap-1.5">
          {#each queues as q, i (q.id)}
            {@const open = selected === q.id}
            <li
              class="flex flex-wrap items-center gap-2 bg-card rounded-md border px-3 py-1.5 {open ? 'border-accent' : 'border-transparent'}"
            >
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
                {@const count = $locale && (q.default ? m.queues_default_count() : m.queues_count({ count: q.animes.length }))}
                <!-- The row's name opens the queue's criteria below the list: one click per queue
                     instead of a picker, and the open one is the row with the accent border. -->
                <button
                  type="button"
                  on:click={() => openQueue(q.id)}
                  aria-expanded={open}
                  aria-controls="queue-criteria"
                  aria-label={$locale && m.queues_open({ name: q.name })}
                  class="flex flex-1 min-w-0 items-center gap-2 text-left"
                >
                  <!-- On a phone the count and the Unsaved chip go under the name: beside it, "Every
                       anime not in another queue" left the name a few letters and ran over Rename. -->
                  <span class="min-w-0 flex-1">
                    <span class="flex min-w-0 items-center gap-2">
                      <span class="min-w-0 truncate text-sm text-heading">{q.name}</span>
                      {#if changed.includes(q.id)}
                        <span class="hidden shrink-0 rounded-full bg-control px-1.5 text-[11px] text-subtle sm:inline">{$locale && m.queues_unsaved()}</span>
                      {/if}
                    </span>
                    <span class="flex min-w-0 items-center gap-2 sm:hidden">
                      <span class="min-w-0 truncate text-xs text-subtle">{count}</span>
                      {#if changed.includes(q.id)}
                        <span class="shrink-0 rounded-full bg-control px-1.5 text-[11px] text-subtle">{$locale && m.queues_unsaved()}</span>
                      {/if}
                    </span>
                  </span>
                  <span class="hidden shrink-0 text-xs text-subtle sm:inline">{count}</span>
                  <span class="shrink-0 text-xs font-medium {open ? 'text-accent' : 'text-subtle'}" aria-hidden="true">
                    <span class="hidden sm:inline">{$locale && m.queues_criteria()}</span> {open ? "▾" : "▸"}
                  </span>
                </button>
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
      </div>
    {/if}

    {#if current && selected}
      <section id="queue-criteria" class="space-y-4 scroll-mt-4" aria-label={$locale && m.queues_criteria_title({ name: currentQueue?.name ?? "" })}>
        {#if several && currentQueue}
          <div class="flex items-center justify-between gap-2">
            <h2 class="min-w-0 truncate text-base font-semibold text-heading">
              {$locale && m.queues_criteria_title({ name: currentQueue.name })}
            </h2>
            <button
              type="button"
              on:click={() => (selected = null)}
              class="shrink-0 text-xs font-medium text-subtle hover:text-heading transition-colors"
            >{$locale && m.queues_criteria_close()}</button>
          </div>
        {/if}
        {#if selected !== DEFAULT_QUEUE_ID}
          <p class="text-xs text-subtle">{$locale && m.queues_ignore_shared()}</p>
        {/if}
        <PriorityListsEditor
          ranking={current}
          {defaults}
          showIgnore={selected === DEFAULT_QUEUE_ID}
          on:change={(e) => selected && setRanking(selected, e.detail)}
        />
      </section>
    {:else if several}
      <p class="text-sm text-subtle">{$locale && m.queues_pick_one()}</p>
    {/if}

    {#if several}
      <details class="rounded-card border border-default bg-sunken" data-testid="same-criteria" on:toggle={onSameToggle}>
        <summary class="cursor-pointer select-none px-5 py-4 text-sm font-semibold text-subtle uppercase tracking-wider hover:text-heading">
          {$locale && m.queues_same_title()}
        </summary>
        <!-- Rendered only while open: a second copy of every list in the DOM would double every
             "Use …" control for screen readers while nobody is using it. -->
        {#if sameOpen && draft}
          <div class="flex flex-col gap-4 border-t border-divider p-5">
            <p class="text-xs text-subtle">{$locale && m.queues_same_hint()}</p>
            <div class="flex flex-wrap items-center gap-2">
              <label for="same_from" class="text-xs text-subtle">{$locale && m.queues_same_start()}</label>
              <select
                id="same_from"
                value={sameFrom}
                on:change={(e) => startFrom(e.currentTarget.value)}
                class="rounded-md border border-default bg-control px-3 py-1.5 text-sm text-body"
              >
                {#each queues as q (q.id)}
                  <option value={q.id}>{q.name}</option>
                {/each}
              </select>
            </div>
            <PriorityListsEditor ranking={draft} {defaults} showIgnore={false} on:change={(e) => (draft = e.detail)} />
            <fieldset class="flex flex-col gap-2">
              <legend class="text-xs text-subtle">{$locale && m.queues_same_targets()}</legend>
              <div class="flex flex-wrap gap-x-4 gap-y-2">
                <Checkbox
                  checked={ticked.length === queues.length}
                  indeterminate={ticked.length > 0 && ticked.length < queues.length}
                  label={$locale && m.queues_same_all()}
                  on:change={() => tickAll(ticked.length !== queues.length)}
                />
                {#each queues as q (q.id)}
                  <Checkbox
                    checked={!!targets[q.id]}
                    label={q.name}
                    on:change={() => (targets = { ...targets, [q.id]: !targets[q.id] })}
                  />
                {/each}
              </div>
            </fieldset>
            <div class="flex justify-end">
              <button
                type="button"
                on:click={applySame}
                disabled={ticked.length === 0}
                class="inline-flex items-center px-4 py-2 rounded-lg border border-default text-body hover:bg-control font-medium text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >{$locale && m.queues_same_apply({ count: ticked.length })}</button>
            </div>
          </div>
        {/if}
      </details>
    {/if}

    <ConfirmDialog
      bind:open={deleteOpen}
      title={$locale && deleting ? m.queues_delete_title({ name: deleting.name }) : ""}
      message={$locale && m.queues_delete_msg()}
      confirmLabel={$locale && m.queues_delete_confirm()}
      cancelLabel={$locale && m.common_cancel()}
      on:confirm={confirmDelete}
    />

    <div class="flex justify-end gap-3 pt-2">
      {#if current}
        <button
          type="button"
          on:click={resetAll}
          disabled={saving}
          class="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-default text-body hover:bg-control font-medium text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {T.resetAll}
        </button>
      {/if}
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
