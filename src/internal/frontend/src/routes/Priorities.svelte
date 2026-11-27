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
  import { applyPreset } from "../lib/domain/priorityPresets.js";
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
  // their queue. The page lists the queues first. A click on a queue opens its criteria; ticking
  // queues (or Select all) edits them together: a change to a list gives that list to every queue
  // ticked, and the lists left alone stay each queue's own. With a single queue (or none readable)
  // its criteria are open from the start.
  let queues: Queue[] = [];
  let selection: string[] = [DEFAULT_QUEUE_ID];
  let rankings: Record<string, Priorities> = {};
  // What was loaded (or last saved), per queue: dirty means "differs from it", so undoing a change
  // clears it.
  let baselines: Record<string, string> = {};
  let newQueueName = "";
  let renaming: string | null = null;
  let renameValue = "";
  let deleting: Queue | null = null;
  let deleteOpen = false;

  $: several = queues.length > 1;
  // The queues being edited, in the queues' order; the first one's lists are the ones shown.
  $: editing = queues.length > 0 ? queues.filter((q) => selection.includes(q.id)) : [];
  $: editingIds = queues.length > 0 ? editing.map((q) => q.id) : selection;
  $: current = editingIds.length > 0 ? (rankings[editingIds[0]] ?? null) : null;
  // What the editor shows: the first queue's lists, with the block list always the default
  // queue's. Another queue's copy is never read (decisions/164), and Default may sit below it.
  $: shown = current && editingIds.includes(DEFAULT_QUEUE_ID) ? { ...current, ignore_list: rankings[DEFAULT_QUEUE_ID].ignore_list } : current;
  $: differs = listsThatDiffer(editingIds, rankings);
  $: changed = Object.keys(rankings).filter((id) => snapshot(rankings[id]) !== baselines[id]);
  $: setUnsaved("priorities", changed.length > 0);
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
      selection = queues.length > 1 ? [] : [DEFAULT_QUEUE_ID];
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.priorities_toast_load_error());
    } finally {
      loading = false;
    }
  }

  /** The lists (the shared block list aside) whose value isn't the same in every queue edited. */
  function listsThatDiffer(ids: string[], all: Record<string, Priorities>): (keyof Priorities)[] {
    if (ids.length < 2) return [];
    const first = all[ids[0]];
    if (!first) return [];
    return (Object.keys(first) as (keyof Priorities)[]).filter(
      (key) => key !== "ignore_list" && ids.some((id) => all[id] && snapshot(all[id][key]) !== snapshot(first[key])),
    );
  }

  /**
   * Gives the list to every queue edited. The block list is the default queue's alone (every queue
   * shares it): it goes to that queue only.
   */
  function setList(key: keyof Priorities, list: string[]) {
    for (const id of editingIds) {
      if (key === "ignore_list" && id !== DEFAULT_QUEUE_ID) continue;
      rankings[id] = { ...rankings[id], [key]: [...list] };
    }
    rankings = rankings;
  }

  /** A preset promotes tokens in each queue's own list, so none loses one it added by hand. */
  function applyListPreset(key: keyof Priorities, first: string[]) {
    for (const id of editingIds) {
      rankings[id] = { ...rankings[id], [key]: applyPreset(rankings[id][key], first) };
    }
    rankings = rankings;
  }

  /** Opens a queue's criteria alone, or closes them when they are the only ones open. */
  async function openQueue(id: string) {
    if (selection.length === 1 && selection[0] === id) {
      if (several) selection = [];
      return;
    }
    selection = [id];
    await tick();
    document.getElementById("queue-criteria")?.scrollIntoView?.({ block: "start", behavior: "smooth" });
  }

  function toggleQueue(id: string) {
    selection = selection.includes(id) ? selection.filter((x) => x !== id) : [...selection, id];
  }

  function selectAll(on: boolean) {
    selection = on ? queues.map((q) => q.id) : [];
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
      selection = [q.id];
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
      selection = queues.length > 1 ? selection.filter((x) => x !== q.id) : [DEFAULT_QUEUE_ID];
      toast.success(m.queues_toast_deleted({ name: q.name, count: moved_to_default }));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.queues_toast_error());
    }
  }

  function resetAll() {
    if (!defaults) return;
    // A queue's ignore list is not its own: reset only touches what it ranks with.
    for (const id of editingIds) {
      rankings[id] = id === DEFAULT_QUEUE_ID ? clone(defaults) : { ...clone(defaults), ignore_list: rankings[id].ignore_list };
    }
    rankings = rankings;
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
        {#if several}
          <div class="px-3">
            <Checkbox
              checked={editing.length === queues.length}
              indeterminate={editing.length > 0 && editing.length < queues.length}
              label={$locale && m.queues_select_all()}
              on:change={() => selectAll(editing.length !== queues.length)}
            />
          </div>
        {/if}
        <ol class="flex flex-col gap-1.5">
          {#each queues as q, i (q.id)}
            {@const open = editingIds.includes(q.id)}
            <li
              class="flex flex-wrap items-center gap-2 bg-card rounded-md border px-3 py-1.5 {open ? 'border-accent' : 'border-transparent'}"
            >
              {#if several}
                <!-- Ticking queues edits them together; the name below opens one queue alone. -->
                <Checkbox
                  checked={open}
                  label={$locale && m.queues_select({ name: q.name })}
                  labelHidden
                  on:change={() => toggleQueue(q.id)}
                />
              {/if}
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
                <!-- The row's name opens the queue's criteria alone, below the list; the queues
                     being edited are the rows with the accent border. -->
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
                    <span class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 sm:hidden">
                      <span class="min-w-0 text-xs text-subtle">{count}</span>
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

    {#if current}
      {@const names = editing.map((q) => q.name)}
      <section
        id="queue-criteria"
        class="space-y-4 scroll-mt-4"
        aria-label={$locale && (names.length > 1 ? m.queues_criteria_title_many({ count: names.length }) : m.queues_criteria_title({ name: names[0] ?? "" }))}
      >
        {#if several && names.length > 0}
          <div class="flex items-center justify-between gap-2">
            <h2 class="min-w-0 truncate text-base font-semibold text-heading">
              {$locale && (names.length > 1 ? m.queues_criteria_title_many({ count: names.length }) : m.queues_criteria_title({ name: names[0] }))}
            </h2>
            <button
              type="button"
              on:click={() => (selection = [])}
              class="shrink-0 text-xs font-medium text-subtle hover:text-heading transition-colors"
            >{$locale && m.queues_criteria_close()}</button>
          </div>
        {/if}
        {#if names.length > 1}
          <!-- Loud on purpose: every click below changes several queues at once. -->
          <p role="status" class="rounded-card border border-warn-tint/28 bg-warn-tint/12 px-4 py-3 text-sm text-warn" data-testid="several-queues-note">
            {$locale && m.queues_several_note({ count: names.length, names: names.join(", ") })}
          </p>
        {/if}
        {#if !editingIds.includes(DEFAULT_QUEUE_ID)}
          <p class="text-xs text-subtle">{$locale && m.queues_ignore_shared()}</p>
        {/if}
        <PriorityListsEditor
          ranking={shown ?? current}
          {defaults}
          showIgnore={editingIds.includes(DEFAULT_QUEUE_ID)}
          {differs}
          differsNote={$locale && names.length > 1 ? m.queues_list_differs({ name: names[0] }) : ""}
          ignoreNote={$locale && names.length > 1 ? m.queues_block_list_one() : ""}
          on:change={(e) => setList(e.detail.key, e.detail.list)}
          on:preset={(e) => applyListPreset(e.detail.key, e.detail.first)}
        />
      </section>
    {:else if several}
      <p class="text-sm text-subtle">{$locale && m.queues_pick_one()}</p>
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
