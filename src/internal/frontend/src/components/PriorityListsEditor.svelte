<script lang="ts">
  // The criteria lists the Priorities page edits, for one queue or for several queues edited
  // together. It never keeps the ranking: every edit goes back to the page as `change` with the
  // one list it touched, and the page gives that list to every queue being edited.
  import { createEventDispatcher } from "svelte";
  import type { Priorities } from "../lib/api/client.js";
  import Checkbox from "./ui/Checkbox.svelte";
  import { PRESETS } from "../lib/domain/priorityPresets.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";

  export let ranking: Priorities;
  export let defaults: Priorities | null;
  /** The ignore list is the default queue's alone: every queue shares it (decisions/164). */
  export let showIgnore = true;
  /** Lists that differ between the queues edited together, and the line that says so. */
  export let differs: (keyof Priorities)[] = [];
  export let differsNote = "";
  /** A line under the block list: with several queues ticked, a change to it reaches one list. */
  export let ignoreNote = "";

  // `preset` is its own event: with several queues edited together, each queue's own list is
  // promoted, so a token one of them added by hand isn't lost (decisions/070).
  const dispatch = createEventDispatcher<{
    change: { key: keyof Priorities; list: string[] };
    preset: { key: keyof Priorities; first: string[] };
  }>();

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
    resetList: m.priorities_reset_list(),
    addPlaceholder: m.priorities_add_placeholder(),
    lists: LISTS.map((l) => ({ ...l, label: l.label(), scope: l.scope && l.scope() })),
    notes: Object.fromEntries(Object.entries(NOTES).map(([k, f]) => [k, f()])) as Partial<Record<keyof Priorities, string>>,
    presets: Object.fromEntries(
      Object.entries(PRESETS).map(([k, list]) => [k, (list ?? []).map((p) => ({ ...p, label: p.label(), desc: p.desc() }))]),
    ) as Partial<Record<keyof Priorities, { key: string; label: string; desc: string; first: string[] }[]>>,
  };

  $: lists = T ? T.lists.filter((l) => showIgnore || l.key !== "ignore_list") : [];

  let newItem: Record<string, string> = {};

  /** Replaces one list of the ranking. */
  function setList(key: keyof Priorities, list: string[]) {
    dispatch("change", { key, list });
  }

  function move(key: keyof Priorities, i: number, dir: -1 | 1) {
    const list = [...ranking[key]];
    const j = i + dir;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    setList(key, list);
  }

  /**
   * The list's rows: what is saved (checked, in order) followed by the canonical tokens left out
   * (unchecked, at the end). A token missing from the list is already treated as the worst by the
   * backend (`priorityIndex` returns `len(list)`), so "unchecked at the end" is exactly what
   * unchecking means — and nothing disappears from the screen, which was the problem with the X.
   *
   * `custom` = not in the default: what the user typed, plus the inert leftovers of an old
   * config.json (`x265`, `4k`). Only those can be removed for good (decisions/070).
   */
  function rows(key: keyof Priorities, list: string[], def: Priorities | null) {
    const canon = def?.[key] ?? [];
    return [
      ...list.map((item) => ({ item, on: true, custom: !canon.includes(item) })),
      ...canon.filter((v) => !list.includes(v)).map((item) => ({ item, on: false, custom: false })),
    ];
  }

  function toggle(key: keyof Priorities, item: string, on: boolean) {
    setList(key, on ? [...ranking[key], item] : ranking[key].filter((v) => v !== item));
  }

  function remove(key: keyof Priorities, i: number) {
    setList(key, ranking[key].filter((_, idx) => idx !== i));
  }

  function add(key: keyof Priorities) {
    const v = (newItem[key] ?? "").trim().toLowerCase();
    if (!v || ranking[key].includes(v)) return;
    setList(key, [...ranking[key], v]);
    newItem[key] = "";
  }

  function preset(key: keyof Priorities, first: string[]) {
    dispatch("preset", { key, first });
  }

  function resetList(key: keyof Priorities) {
    if (!defaults) return;
    setList(key, [...defaults[key]]);
  }
</script>

{#if T}
  <div class="space-y-4">
    {#each lists as { key, label, scope } (key)}
      {@const items = rows(key, ranking[key], defaults)}
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

          {#if differsNote && differs.includes(key)}
            <p class="text-xs text-warn">{differsNote}</p>
          {/if}
          {#if ignoreNote && key === "ignore_list"}
            <p class="text-xs text-warn">{ignoreNote}</p>
          {/if}

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
                      disabled={i === ranking[key].length - 1}
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
{/if}
