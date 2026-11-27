<script lang="ts">
  // Shoko panel of the anime detail screen: how the anime lines up with the user's Shoko library
  // in the last pass, and the match set by hand (decisions/146). A partial match owns nothing until
  // it is confirmed here, so it shows the series and offset to confirm.
  import {
    updateAnimeShoko,
    updateAnimeShokoPacks,
    type AnimeShokoInfo,
    type ShokoMatchRequest,
    type ShokoPackEpisodes,
  } from "../lib/api/client.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import { toast } from "../lib/stores/toast.js";
  import Input from "./Input.svelte";
  import Button from "./ui/Button.svelte";

  export let animeId: number;
  export let shoko: AnimeShokoInfo | undefined;

  let seriesId = 0;
  let offset = 0;
  let saving = false;
  // The manual form stays behind "Correct the match" for a clean match: it pushed the episode list
  // down on every anime (UI review of 2026-11-25). A partial match to confirm, or none to set by
  // hand, shows it right away.
  let correcting = false;
  $: formOpen = correcting || shoko?.status === "partial" || shoko?.status === "none";
  $: if (shoko) {
    seriesId = shoko.series_id ?? 0;
    offset = shoko.offset ?? 0;
  }

  // What a pack chosen for this anime does with the episodes in Shoko it brings (decisions/153):
  // the global setting, or the anime's own choice.
  type PackChoice = ShokoPackEpisodes | "default";
  let packChoice: PackChoice = "default";
  $: packChoice = shoko?.pack_episodes ?? "default";
  $: packOptions = [
    {
      id: "default" as PackChoice,
      label: m.detail_shoko_packs_default({
        choice: shoko?.keep_pack_episodes_default ? m.detail_shoko_packs_download() : m.detail_shoko_packs_remove(),
      }),
    },
    { id: "download" as PackChoice, label: m.detail_shoko_packs_download() },
    { id: "remove" as PackChoice, label: m.detail_shoko_packs_remove() },
  ];

  async function savePacks(choice: PackChoice) {
    const previous = packChoice;
    packChoice = choice;
    saving = true;
    try {
      await updateAnimeShokoPacks(animeId, choice);
      toast.success(m.detail_shoko_saved());
    } catch (err) {
      packChoice = previous;
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }

  async function save(match: ShokoMatchRequest) {
    saving = true;
    try {
      await updateAnimeShoko(animeId, match);
      toast.success(m.detail_shoko_saved());
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }
</script>

{#if shoko}
  <div class="space-y-3 rounded-card border border-default bg-card p-4.5" data-testid="shoko-panel">
    <p class="text-label text-subtle">{$locale && m.detail_shoko_title()}</p>
    <p class="text-copy text-body" data-testid="shoko-panel-status">
      {#if shoko.status === "matched"}
        {$locale && m.detail_shoko_matched({ series: shoko.series_name ?? String(shoko.series_id) })}
        {#if shoko.offset}
          {$locale && m.detail_shoko_offset({ n: String((shoko.offset ?? 0) + 1) })}
        {/if}
      {:else if shoko.status === "partial"}
        {$locale && m.detail_shoko_partial({ series: shoko.series_name ?? String(shoko.series_id) })}
      {:else}
        {$locale && m.detail_shoko_none()}
      {/if}
      {#if shoko.by_hand}
        {$locale && m.detail_shoko_by_hand()}
      {/if}
    </p>
    <!-- A partial match to confirm, a wrong one to correct, or no match at all to set by hand (a
         series that shares no MAL id with the anime). -->
    {#if formOpen}
    <div class="flex flex-col gap-2 md:flex-row md:items-end" data-testid="shoko-match-form">
        <div class="flex-1">
          <Input id="shoko_series_id" type="number" min="1" label={$locale && m.detail_shoko_series_id()} bind:value={seriesId} />
        </div>
        <div class="flex-1">
          <Input id="shoko_offset" type="number" min="0" label={$locale && m.detail_shoko_offset_label()} bind:value={offset} />
        </div>
        <Button variant="ghost" disabled={saving || seriesId < 1 || offset < 0} on:click={() => save({ series_id: Number(seriesId), offset: Number(offset) })}>
          {$locale && m.detail_shoko_confirm()}
        </Button>
      </div>
    <!-- Under the row, not as the Offset field's subtitle: a taller column would move the row's
         bottom-aligned fields off the inputs. -->
    <p class="text-caption text-subtle">{$locale && m.detail_shoko_offset_hint()}</p>
    {/if}
    <div class="flex flex-wrap gap-2">
      {#if !formOpen}
        <Button variant="ghost" on:click={() => (correcting = true)}>{$locale && m.detail_shoko_correct()}</Button>
      {/if}
      {#if !(shoko.status === "none" && shoko.by_hand)}
        <Button variant="ghost" disabled={saving} on:click={() => save({ none: true })}>{$locale && m.detail_shoko_not_in()}</Button>
      {/if}
      {#if shoko.by_hand}
        <Button variant="ghost" disabled={saving} on:click={() => save({ auto: true })}>{$locale && m.detail_shoko_auto()}</Button>
      {/if}
    </div>
    <div class="space-y-1.5 border-t border-default pt-3">
      <p class="text-label text-subtle" id="shoko-packs-label">{$locale && m.detail_shoko_packs_label()}</p>
      {#key $locale}
      <div class="inline-flex max-w-full flex-wrap overflow-hidden rounded-pill border border-neutral/45" role="radiogroup" aria-labelledby="shoko-packs-label" data-testid="shoko-packs">
        {#each packOptions as opt, i (opt.id)}
          <button
            type="button"
            role="radio"
            aria-checked={packChoice === opt.id}
            disabled={saving}
            class="inline-flex items-center px-3 py-1.5 text-caption font-semibold transition-colors {i > 0
              ? 'border-l border-neutral/45'
              : ''} {packChoice === opt.id ? 'bg-accent-tint/12 text-accent' : 'bg-control text-body hover:text-heading'}"
            on:click={() => savePacks(opt.id)}
          >
            {opt.label}
          </button>
        {/each}
      </div>
      {/key}
      <p class="text-caption text-subtle">{$locale && m.detail_shoko_packs_hint()}</p>
    </div>
  </div>
{/if}
