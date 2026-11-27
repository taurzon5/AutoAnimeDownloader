<script lang="ts">
  // The anime page's download queue (decisions/164): shown once the user made a second queue. The
  // anime searches in its queue's turn and ranks with its priorities.
  import { getQueues, moveAnimeToQueue, type Queue } from "../lib/api/client.js";
  import { queueOfAnime } from "../lib/domain/queues.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import { toast } from "../lib/stores/toast.js";

  export let animeId: number;

  let queues: Queue[] = [];
  let chosen = "";
  let saving = false;

  async function load(id: number) {
    try {
      queues = (await getQueues()) ?? [];
      chosen = queueOfAnime(queues, id)?.id ?? "";
    } catch {
      queues = [];
    }
  }
  $: load(animeId);

  async function choose(e: Event) {
    const value = (e.target as HTMLSelectElement).value;
    const previous = chosen;
    chosen = value;
    saving = true;
    try {
      await moveAnimeToQueue(animeId, value);
      toast.success(m.detail_queue_saved({ name: queues.find((q) => q.id === value)?.name ?? value }));
    } catch (err) {
      chosen = previous;
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      saving = false;
    }
  }
</script>

{#if queues.length > 1}
  <div class="space-y-1.5 rounded-card border border-default bg-card p-4.5" data-testid="anime-queue-picker">
    <label for="anime_queue" class="text-label text-subtle">{$locale && m.detail_queue_label()}</label>
    <select
      id="anime_queue"
      class="w-full rounded-field border border-default bg-control px-3 py-2 text-copy text-body"
      value={chosen}
      disabled={saving}
      on:change={choose}
    >
      {#each queues as q (q.id)}
        <option value={q.id}>{q.name}</option>
      {/each}
    </select>
    <p class="text-caption text-subtle">{$locale && m.detail_queue_hint()}</p>
  </div>
{/if}
