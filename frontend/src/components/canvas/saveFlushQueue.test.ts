import { describe, expect, it } from "vitest";
import { createSaveFlushQueue } from "./saveFlushQueue";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("createSaveFlushQueue", () => {
  it.each([true, false])(
    "holds an overlapping caller and shares the preceding %s result",
    async (saveResult) => {
      const save = deferred<boolean>();
      const events: string[] = [];
      const queue = createSaveFlushQueue();

      const first = queue.run(async () => {
        events.push("first started");
        return save.promise;
      });
      const second = queue.run(async () => {
        events.push("second started");
        return null;
      });

      await Promise.resolve();
      expect(events).toEqual(["first started"]);

      save.resolve(saveResult);
      await expect(first).resolves.toBe(saveResult);
      await expect(second).resolves.toBe(saveResult);
      expect(events).toEqual(["first started", "second started"]);
    },
  );

  it("lets newer save work replace an earlier failure", async () => {
    const queue = createSaveFlushQueue();

    const first = queue.run(async () => false);
    const second = queue.run(async () => true);

    await expect(first).resolves.toBe(false);
    await expect(second).resolves.toBe(true);
  });

  it("uses the latest background save result when a flush has no work", async () => {
    const queue = createSaveFlushQueue();

    queue.record(false);
    await expect(queue.run(async () => null)).resolves.toBe(false);

    queue.record(true);
    await expect(queue.run(async () => null)).resolves.toBe(true);
  });
});
