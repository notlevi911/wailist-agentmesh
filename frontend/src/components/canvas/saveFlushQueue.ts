export type SaveFlushAttempt = () => Promise<boolean | null>;

export interface SaveFlushQueue {
  record: (result: boolean) => void;
  run: (attempt: SaveFlushAttempt) => Promise<boolean>;
}

export function createSaveFlushQueue(): SaveFlushQueue {
  let tail: Promise<void> = Promise.resolve();
  let latestResult = true;

  return {
    record(result) {
      latestResult = result;
    },
    run(attempt) {
      const result = tail.then(async () => {
        const attemptedResult = await attempt();
        // A queued caller with no new save work shares the result of the flush
        // immediately ahead of it instead of assuming persistence succeeded.
        if (attemptedResult !== null) latestResult = attemptedResult;
        return latestResult;
      });
      tail = result.then(
        () => undefined,
        () => undefined,
      );
      return result;
    },
  };
}
