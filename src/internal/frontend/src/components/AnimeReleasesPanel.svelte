<script lang="ts">
  // Torrents panel of the anime detail screen: one card per torrent that brought this anime's
  // episodes, with the release it was, how it was added, its tags and its state now (the
  // release-filters spec §9, decisions/150). Loads on its own: the episode table must not wait for it.
  import { getAnimeReleases, type AnimeReleaseCard, type ReleaseTags } from "../lib/api/client.js";
  import { formatBytes, formatDate, formatPercent, type FormatLocale } from "../lib/domain/format.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import Chip from "./ui/Chip.svelte";

  export let animeId: number;
  /** Changes when the page reloads its data (after an action): the cards are read again. */
  export let tick: unknown = 0;

  let cards: AnimeReleaseCard[] = [];
  let failed = false;
  $: fmtLocale = ($locale ?? "en") as FormatLocale;

  async function load(id: number, _tick: unknown) {
    if (!id || id <= 0) return;
    try {
      cards = await getAnimeReleases(id);
      failed = false;
    } catch {
      failed = true;
    }
  }
  $: load(animeId, tick);

  /** "5", "1–12", or "1–3, 5" when there are gaps. */
  function episodeSpan(eps: number[]): string {
    const parts: string[] = [];
    for (let i = 0; i < eps.length; ) {
      let j = i;
      while (j + 1 < eps.length && eps[j + 1] === eps[j] + 1) j++;
      parts.push(i === j ? String(eps[i]) : `${eps[i]}–${eps[j]}`);
      i = j + 1;
    }
    return parts.join(", ");
  }

  type Tag = { text: string; assumed: boolean };
  /** The name's tags as chips; an assumed value says so. */
  function tags(r: ReleaseTags): Tag[] {
    const assumed = (field: string) => (r.assumed ?? []).includes(field);
    const out: Tag[] = [];
    const push = (text: string | undefined, field = "") => {
      if (text) out.push({ text, assumed: field !== "" && assumed(field) });
    };
    push(r.resolution);
    push(r.source && [r.source, r.service].filter(Boolean).join(" ").toUpperCase(), "source");
    push(r.video_codec?.toUpperCase());
    push(r.bit_depth ? `${r.bit_depth}-bit` : undefined);
    push([r.audio_codec?.toUpperCase(), r.audio_channels].filter(Boolean).join(" "));
    if (r.audio_languages?.length) push(m.detail_releases_audio({ langs: r.audio_languages.join(" + ").toUpperCase() }), "audio_languages");
    if (r.hardsub) push(m.detail_releases_hardsub());
    else if (r.subtitle_languages?.length) push(m.detail_releases_subs({ langs: r.subtitle_languages.join(" + ").toUpperCase() }), "subtitle_languages");
    if (r.version && r.version > 1) push(`v${r.version}`);
    if (r.repack) push("REPACK");
    if (r.uncensored) push(m.detail_releases_uncensored());
    return out;
  }

  function stateLabel(c: AnimeReleaseCard): string {
    switch (c.state) {
      case "downloading":
        return m.detail_releases_state_downloading({ percent: formatPercent(c.progress, fmtLocale) });
      case "queued":
        return m.detail_releases_state_queued();
      case "seeding":
        return m.detail_releases_state_seeding();
      case "stopped":
        return m.detail_releases_state_stopped();
      case "released":
        return m.detail_releases_state_released();
      case "missing":
        return m.detail_releases_state_missing();
      default:
        return m.detail_releases_state_removed();
    }
  }

  function stateVariant(c: AnimeReleaseCard): "accent" | "ok" | "warn" | "danger" | "neutral" {
    if (c.state === "seeding" || c.state === "released") return "ok";
    if (c.state === "downloading" || c.state === "queued") return "accent";
    if (c.state === "missing") return "danger";
    return "neutral";
  }

  function originLabel(c: AnimeReleaseCard): string {
    if (!c.recorded) return m.detail_releases_origin_unknown();
    switch (c.origin) {
      case "manual":
        return m.detail_releases_origin_manual();
      case "picker":
        return m.detail_releases_origin_picker();
      case "magnet":
        return m.detail_releases_origin_magnet();
      default:
        return m.detail_releases_origin_search();
    }
  }
</script>

{#if cards.length > 0 || failed}
  <section class="rounded-card border border-default bg-card p-4" aria-labelledby="anime-releases-title" data-testid="releases-panel">
    <h2 id="anime-releases-title" class="text-card-title text-heading">{$locale && m.detail_releases_title()}</h2>
    {#if failed}
      <p class="mt-2 text-caption text-danger">{$locale && m.detail_releases_load_error()}</p>
    {/if}
    {#key $locale}
    <ul class="mt-3 space-y-2">
      {#each cards as card (card.hash)}
        <li class="rounded-field bg-control px-3 py-2.5 {card.state === 'removed' ? 'opacity-70' : ''}" data-testid="release-card">
          <!-- The state chip doesn't wrap: on a phone it goes above the name, which then gets the whole
               width (beside it, pt-BR's "Na biblioteca, sem semear" left the name a third). -->
          <div class="flex flex-col-reverse items-start gap-1.5 sm:flex-row sm:justify-between sm:gap-2">
            <div class="min-w-0 w-full sm:w-auto sm:flex-1">
              <p class="break-words text-copy font-semibold text-heading">
                {#if card.detail_url}
                  <a href={card.detail_url} target="_blank" rel="noopener noreferrer" class="hover:underline">{card.name || card.hash}</a>
                {:else}
                  {card.name || card.hash}
                {/if}
              </p>
              <p class="mt-0.5 text-caption text-subtle">
                {$locale && (card.episodes.length === 1
                  ? m.detail_releases_episode({ n: String(card.episodes[0]) })
                  : m.detail_releases_episodes({ span: episodeSpan(card.episodes) }))}
                {#if card.release.group}· {card.release.group}{/if}
                {#if card.size}· {formatBytes(card.size, fmtLocale)}{/if}
              </p>
            </div>
            <Chip variant={stateVariant(card)}>{$locale && stateLabel(card)}</Chip>
          </div>
          <div class="mt-2 flex flex-wrap gap-1.5" data-testid="release-tags">
            {#each tags(card.release) as tag}
              <span
                class="rounded-badge border border-default px-1.5 py-0.5 font-mono text-[11.5px] {tag.assumed ? 'border-dashed text-subtle' : 'text-body'}"
                title={tag.assumed ? m.detail_releases_assumed() : undefined}
              >{tag.text}{tag.assumed ? "?" : ""}</span>
            {/each}
            {#if card.trusted}<Chip variant="ok">{$locale && m.detail_releases_trusted()}</Chip>{/if}
            {#if card.remake}<Chip variant="warn">{$locale && m.detail_releases_remake()}</Chip>{/if}
          </div>
          <p class="mt-2 text-caption text-subtle">
            {$locale && originLabel(card)}
            {#if card.added_at}· {$locale && m.detail_releases_added({ when: formatDate(card.added_at, fmtLocale) })}{/if}
            {#if card.uploaded_at}· {$locale && m.detail_releases_uploaded({ when: formatDate(card.uploaded_at, fmtLocale) })}{/if}
            {#if card.seeders}· {$locale && m.detail_releases_seeders({ seeders: String(card.seeders), leechers: String(card.leechers ?? 0) })}{/if}
          </p>
        </li>
      {/each}
    </ul>
    {/key}
  </section>
{/if}
