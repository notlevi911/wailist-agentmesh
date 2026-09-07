package ai.agentmesh.app;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

/**
 * Persists every currently-armed geofence's parameters so BootReceiver can
 * re-register all of them after a reboot, when there is no WebView alive to
 * ask GeofencePlugin's caller for them.
 *
 * Keyed by workflow id rather than one slot: GeofencingClient.addGeofences()
 * is additive, not a replace, so nothing stops a caller from arming a second
 * workflow's fence before disarming the first. A single-slot store would
 * silently drop the first fence's reboot recovery the moment that happened.
 */
final class GeofenceStore {

    private static final String PREFS = "AgentMeshGeofenceStore";
    private static final String KEY_FENCES = "fences";

    // Guards every read-modify-write of KEY_FENCES below. save()/remove() are
    // called from async GMS Task callbacks (GeofencePlugin's addGeofence and
    // removeGeofence success listeners), which can run back-to-back for
    // different workflows, and BootReceiver.loadAll() can run concurrently
    // with either. Without this, two calls can both read the same starting
    // JSONObject before either writes, and the second write wins outright --
    // silently dropping the other workflow's entry, exactly the single-slot
    // clobber this keyed store exists to eliminate.
    private static final Object LOCK = new Object();

    private GeofenceStore() {
    }

    static void save(Context context, String id, double lat, double lng, double radiusM) {
        synchronized (LOCK) {
            SharedPreferences p = prefs(context);
            JSONObject fences = readAll(p);
            try {
                JSONObject fence = new JSONObject();
                fence.put("lat", lat);
                fence.put("lng", lng);
                fence.put("radiusM", radiusM);
                fences.put(id, fence);
            } catch (JSONException e) {
                return;
            }
            // commit(), not apply(): a reboot landing between this call
            // returning and an async apply() reaching disk means
            // BootReceiver won't find this fence in loadAll() and silently
            // fails to re-register it after restart -- the same durability
            // argument GeofenceReceiver.append() makes for its own commit().
            p.edit().putString(KEY_FENCES, fences.toString()).commit();
        }
    }

    // Removes exactly this workflow's fence. Any other workflow's armed fence
    // is untouched, unlike a single clear() that would wipe them all.
    static void remove(Context context, String id) {
        synchronized (LOCK) {
            SharedPreferences p = prefs(context);
            JSONObject fences = readAll(p);
            fences.remove(id);
            // See save()'s comment: commit(), not apply(), so a disarm just
            // before a reboot can't lose the race and have BootReceiver
            // re-register a fence the user just removed.
            p.edit().putString(KEY_FENCES, fences.toString()).commit();
        }
    }

    static List<Active> loadAll(Context context) {
        JSONObject fences;
        synchronized (LOCK) {
            fences = readAll(prefs(context));
        }
        List<Active> out = new ArrayList<>();
        JSONArray ids = fences.names();
        if (ids == null) {
            return out;
        }
        for (int i = 0; i < ids.length(); i++) {
            try {
                String id = ids.getString(i);
                JSONObject fence = fences.getJSONObject(id);
                out.add(new Active(id, fence.getDouble("lat"), fence.getDouble("lng"), fence.getDouble("radiusM")));
            } catch (JSONException e) {
                // A corrupt single entry must not take the rest of the list
                // down with it -- skip it, recover everything else.
            }
        }
        return out;
    }

    private static JSONObject readAll(SharedPreferences p) {
        String raw = p.getString(KEY_FENCES, null);
        if (raw == null) {
            return new JSONObject();
        }
        try {
            return new JSONObject(raw);
        } catch (JSONException e) {
            return new JSONObject();
        }
    }

    private static SharedPreferences prefs(Context context) {
        return context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    static final class Active {
        final String id;
        final double lat;
        final double lng;
        final double radiusM;

        Active(String id, double lat, double lng, double radiusM) {
            this.id = id;
            this.lat = lat;
            this.lng = lng;
            this.radiusM = radiusM;
        }
    }
}
