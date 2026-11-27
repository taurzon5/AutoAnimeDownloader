import type { Queue } from '../api/client.js'

/** The default queue's id (decisions/164): every anime no other queue lists is in it. */
export const DEFAULT_QUEUE_ID = 'default'

/** The queue an anime is in: the one that lists it, else the default queue. */
export function queueOfAnime(queues: Queue[], animeId: number): Queue | undefined {
  return queues.find((q) => !q.default && q.animes.includes(animeId)) ?? queues.find((q) => q.default)
}
