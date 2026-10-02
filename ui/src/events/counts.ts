/**
 * The Events chain's counts.
 *
 * The three-step strip shows one number per step, and reads this. The three
 * lists arrive from three separate loaded stores, so counting them inside the
 * page would put a second, quietly divergent answer next to the first. A count
 * that could not be read at all is `undefined` --
 * blank, never zero -- because "nothing arrived" and "the read failed" are
 * different facts about a deployment, and only the first one is about the
 * operator's events.
 */

/** One count per tab id. A missing entry means that read did not answer. */
export type EventsCounts = Record<string, number | undefined>;

/** The three reads, narrowed to what counting needs so the page's `api` object
 * fits and a test can stand in for it. */
export interface EventsCountReaders {
  captures(limit?: number): Promise<unknown>;
  mappings(): Promise<unknown>;
  bindings(): Promise<unknown>;
}

/** One step's count: the read that answers it, and the list it counts. */
export interface EventsCountSource {
  /** The tab whose chip and strip step show this count. */
  tab: string;
  /** The list field of that read's response. */
  key: string;
  read(readers: EventsCountReaders): Promise<unknown>;
}

// The Inspector tab shows a bounded page of captures, so its count counts that
// same page: a chip reading 400 next to a tab showing 100 rows is a number the
// operator cannot act on.
const CAPTURE_PAGE = 100;

/** One source per step of the chain, in the order the strip reads them. */
export const COUNT_SOURCES: EventsCountSource[] = [
  {
    tab: "inspector",
    key: "captures",
    read: (readers) => readers.captures(CAPTURE_PAGE),
  },
  { tab: "mappings", key: "mappings", read: (readers) => readers.mappings() },
  { tab: "bindings", key: "bindings", read: (readers) => readers.bindings() },
];

/**
 * loadEventCounts reads the three lists and counts each. One read failing
 * leaves its own count blank and does not take the other two down with it: an
 * unreachable capture service must not blank the mappings count.
 */
export async function loadEventCounts(
  readers: EventsCountReaders,
): Promise<EventsCounts> {
  const settled = await Promise.allSettled(
    COUNT_SOURCES.map((source) => source.read(readers)),
  );
  const counts: EventsCounts = {};
  COUNT_SOURCES.forEach((source, i) => {
    const result = settled[i];
    counts[source.tab] =
      result?.status === "fulfilled"
        ? countList(result.value, source.key)
        : undefined;
  });
  return counts;
}

/** countList counts the list a response carries. A response without it -- an
 * older server, a body nobody expected -- answered, and answered with nothing. */
function countList(response: unknown, key: string): number {
  const list = (response as Record<string, unknown> | null | undefined)?.[key];
  return Array.isArray(list) ? list.length : 0;
}
