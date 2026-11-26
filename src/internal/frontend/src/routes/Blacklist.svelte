<script lang="ts">
  // The app's blacklist (decisions/155): anime the daemon no longer checks or downloads, their
  // unfinished torrents paused, their files kept. Entries come from GET /blacklist, which keeps the
  // name and cover itself: an anime deleted and blacklisted has no record left anywhere else.
  // Anime in an AniList Excluded List are not here: that list is changed on AniList.
  import { onMount } from "svelte";
  import { getBlacklist, unblacklistAnime, type BlacklistEntry } from "../lib/api/client.js";
  import Button from "../components/ui/Button.svelte";
  import Cover from "../components/ui/Cover.svelte";
  import Loading from "../components/Loading.svelte";
  import { toast } from "../lib/stores/toast.js";
  import { locale } from "../lib/stores/locale.js";
  import { formatDate, type FormatLocale } from "../lib/domain/format.js";
  import * as m from "../lib/i18n/messages.js";

  let entries: BlacklistEntry[] = [];
  let loading = true;
  let failed = false;
  let removing: Record<number, boolean> = {};

  $: T = $locale && {
    title: m.blacklist_title(),
    intro: m.blacklist_intro(),
    empty: m.blacklist_empty(),
    remove: m.blacklist_remove(),
    loadError: m.blacklist_load_error(),
  };

  function nameOf(e: BlacklistEntry): string {
    return e.name || m.blacklist_unknown_name({ id: e.anime_id });
  }

  async function load(): Promise<void> {
    try {
      entries = await getBlacklist();
      failed = false;
    } catch {
      failed = true;
    } finally {
      loading = false;
    }
  }

  async function lift(e: BlacklistEntry): Promise<void> {
    removing = { ...removing, [e.anime_id]: true };
    try {
      await unblacklistAnime(e.anime_id);
      toast.success(m.blacklist_removed({ name: nameOf(e) }));
      entries = entries.filter((x) => x.anime_id !== e.anime_id);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : m.blacklist_remove());
    } finally {
      removing = { ...removing, [e.anime_id]: false };
    }
  }

  onMount(load);
</script>

<div class="flex flex-col gap-4">
  <div>
    <h1 class="text-screen-title text-heading">{T && T.title}</h1>
    <p class="mt-0.5 max-w-[70ch] text-caption text-subtle">{T && T.intro}</p>
  </div>

  {#if loading}
    <Loading />
  {:else if failed}
    <div class="rounded-card border border-default bg-card p-4 text-copy text-body" role="alert">{T && T.loadError}</div>
  {:else if entries.length === 0}
    <div class="rounded-card border border-default bg-card p-4 text-copy text-body">{T && T.empty}</div>
  {:else}
    <ul class="flex flex-col gap-2" data-testid="blacklist">
      {#each entries as e (e.anime_id)}
        <li class="flex items-center gap-3 rounded-card border border-default bg-card p-3">
          <div class="h-[64px] w-[45px] shrink-0 overflow-hidden">
            <Cover src={e.cover} alt={nameOf(e)} />
          </div>
          <div class="min-w-0 flex-1">
            <a
              href="https://anilist.co/anime/{e.anime_id}"
              target="_blank"
              rel="noopener noreferrer"
              class="block truncate text-copy font-semibold text-heading hover:underline"
            >
              {nameOf(e)}
            </a>
            <p class="mt-0.5 text-caption text-subtle">
              {$locale && m.blacklist_since({ when: formatDate(e.blacklisted_at, $locale as FormatLocale) })}
              {#if e.format}· {e.format}{/if}
              · {$locale && m.blacklist_episodes({ count: e.episodes })}
              {#if e.paused > 0}· {$locale && m.blacklist_paused({ count: e.paused })}{/if}
            </p>
          </div>
          <Button variant="ghost" disabled={removing[e.anime_id]} on:click={() => lift(e)}>
            {T && T.remove}
          </Button>
        </li>
      {/each}
    </ul>
  {/if}
</div>
