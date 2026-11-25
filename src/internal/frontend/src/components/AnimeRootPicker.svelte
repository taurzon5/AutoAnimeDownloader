<script lang="ts">
  // The anime page's download folder override (decisions/154): shown only in direct mode with
  // several download folders. New torrents of the anime go to the chosen folder first; a full one
  // passes them to the next in the fill order.
  import { getAnimeSettings, getConfig, updateAnimeRoot, type DownloadRoot } from "../lib/api/client.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import { toast } from "../lib/stores/toast.js";

  export let animeId: number;

  let roots: DownloadRoot[] = [];
  let chosen = "";
  let saving = false;

  async function load(id: number) {
    try {
      const [config, settings] = await Promise.all([getConfig(), getAnimeSettings(id)]);
      roots = config.library_mode === "direct" ? (config.download_roots ?? []) : [];
      chosen = settings?.root ?? "";
    } catch {
      roots = [];
    }
  }
  $: load(animeId);

  async function choose(e: Event) {
    const value = (e.target as HTMLSelectElement).value;
    const previous = chosen;
    chosen = value;
    saving = true;
    try {
      await updateAnimeRoot(animeId, value);
      toast.success(m.detail_root_saved());
    } catch (err) {
      chosen = previous;
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }
</script>

{#if roots.length > 1}
  <div class="space-y-1.5 rounded-card border border-default bg-card p-4.5" data-testid="anime-root-picker">
    <label for="anime_root" class="text-label text-subtle">{$locale && m.detail_root_label()}</label>
    <select
      id="anime_root"
      class="w-full rounded-field border border-default bg-control px-3 py-2 text-copy text-body"
      value={chosen}
      disabled={saving}
      on:change={choose}
    >
      <option value="">{$locale && m.detail_root_default()}</option>
      {#each roots as root (root.name)}
        <option value={root.name}>{root.name} · {root.path}</option>
      {/each}
    </select>
    <p class="text-caption text-subtle">{$locale && m.detail_root_hint()}</p>
  </div>
{/if}
