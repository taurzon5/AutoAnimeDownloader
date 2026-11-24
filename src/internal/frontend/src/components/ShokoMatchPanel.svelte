<script lang="ts">
  // Shoko panel of the anime detail screen: how the anime lines up with the user's Shoko library
  // in the last pass, and the match set by hand (decisions/146). A partial match owns nothing until
  // it is confirmed here, so it shows the series and offset to confirm.
  import { updateAnimeShoko, type AnimeShokoInfo, type ShokoMatchRequest } from "../lib/api/client.js";
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
  $: if (shoko) {
    seriesId = shoko.series_id ?? 0;
    offset = shoko.offset ?? 0;
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
    {#if shoko.status === "partial" || shoko.by_hand}
      <div class="flex flex-col gap-2 md:flex-row md:items-end">
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
    {/if}
    <div class="flex flex-wrap gap-2">
      {#if !(shoko.status === "none" && shoko.by_hand)}
        <Button variant="ghost" disabled={saving} on:click={() => save({ none: true })}>{$locale && m.detail_shoko_not_in()}</Button>
      {/if}
      {#if shoko.by_hand}
        <Button variant="ghost" disabled={saving} on:click={() => save({ auto: true })}>{$locale && m.detail_shoko_auto()}</Button>
      {/if}
    </div>
  </div>
{/if}
