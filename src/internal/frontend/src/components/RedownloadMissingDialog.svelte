<script lang="ts">
  import { createEventDispatcher } from "svelte";
  import ConfirmDialog from "./ConfirmDialog.svelte";
  import Checkbox from "./ui/Checkbox.svelte";
  import type { TorrentInfo } from "../lib/api/client.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";

  // The bulk "redownload missing files" asks first: every torrent with files not found is listed,
  // all ticked, and the user unticks the ones to leave alone (decisions/139).
  export let open = false;
  export let torrents: TorrentInfo[] = [];

  let picked = new Set<string>();

  const dispatch = createEventDispatcher<{ confirm: { hashes: string[] }; cancel: void }>();

  // Every OPENING starts with all ticked, so a previous choice isn't carried over. Only on the
  // opening: the Downloads list refreshes every few seconds, and re-ticking on each refresh would
  // undo what the user unticked while the dialog is up.
  let wasOpen = false;
  $: {
    if (open && !wasOpen) picked = new Set(torrents.map((t) => t.hash));
    wasOpen = open;
  }

  function toggle(hash: string, on: boolean) {
    const next = new Set(picked);
    if (on) next.add(hash);
    else next.delete(hash);
    picked = next;
  }

  function label(t: TorrentInfo): string {
    if (!t.anime_name) return t.name;
    if (t.is_batch || t.episode_number === null) return `${t.anime_name} — ${t.name}`;
    return `${t.anime_name} — ${t.episode_number}`;
  }

  $: T = $locale && {
    title: m.downloads_redownload_missing(),
    confirm: m.downloads_redownload_missing_confirm({ count: picked.size }),
    cancel: m.common_cancel(),
    hint: m.downloads_redownload_missing_hint(),
  };
</script>

<ConfirmDialog
  bind:open
  title={(T && T.title) || ""}
  confirmLabel={(T && T.confirm) || ""}
  cancelLabel={(T && T.cancel) || ""}
  on:confirm={() => dispatch("confirm", { hashes: torrents.filter((t) => picked.has(t.hash)).map((t) => t.hash) })}
  on:cancel={() => dispatch("cancel")}
>
  <div class="space-y-3">
    <p class="text-caption leading-snug text-subtle">{T && T.hint}</p>
    <div class="flex max-h-64 flex-col items-start gap-2 overflow-y-auto" data-testid="redownload-missing-list">
      {#each torrents as t (t.hash)}
        <Checkbox
          checked={picked.has(t.hash)}
          label={label(t)}
          on:change={(e) => toggle(t.hash, (e.target as HTMLInputElement).checked)}
        />
      {/each}
    </div>
  </div>
</ConfirmDialog>
