import { clearTokenIf } from "./auth";
import { clearOptedIn } from "./pushPrefs";
import { disablePush } from "./push";
import { clearAccountGeofences } from "./geofence";

export async function clearDeletedAccount(token: string | null): Promise<void> {
  const results = await Promise.allSettled([
    clearTokenIf(token),
    clearOptedIn(),
    disablePush(),
    clearAccountGeofences(),
  ]);
  const errors = results.filter((result) => result.status === "rejected");
  if (errors.length) throw new AggregateError(errors.map((result) => result.reason), "Device cleanup incomplete");
}
