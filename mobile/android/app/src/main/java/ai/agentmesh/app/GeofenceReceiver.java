package ai.agentmesh.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.location.Location;
import android.util.Log;

import com.google.android.gms.location.Geofence;
import com.google.android.gms.location.GeofencingEvent;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Where a geofence crossing lands when the app is not running.
 *
 * This is the part of the free route that a commercial SDK is really selling:
 * the OS delivers this broadcast whether or not any of our code is alive, and
 * we get a few seconds of execution to do something durable with it. There is
 * no WebView here, so nothing in TypeScript can run.
 *
 * The choice made here is to PERSIST rather than transmit. The crossing is
 * appended to exactly the queue the TypeScript side already drains
 * (src/queue.ts), in exactly the storage @capacitor/preferences reads, so the
 * app flushes it on next launch or next network change without any of that
 * code knowing it came from native.
 *
 * Being honest about the limit: that means a crossing is DELIVERED late -- on
 * next app open -- rather than immediately. Immediate delivery needs an
 * HTTP POST and a WorkManager retry chain written natively, right here.
 * That is the real remaining cost of not paying for the SDK, and it is a
 * contained amount of work rather than an unknown one. The server side
 * already tolerates late and out-of-order fixes by design, so nothing
 * downstream has to change when it is added.
 */
public class GeofenceReceiver extends BroadcastReceiver {

    private static final String TAG = "AgentMeshGeofence";

    // The store @capacitor/preferences uses on Android. Writing here rather
    // than to a private file of our own is what lets the TypeScript side pick
    // these up without a bridge call.
    //
    // Package-private, not private: GeofencePlugin.drainNativeQueue() reads
    // and clears this same key under LOCK below, so both sides need the exact
    // file/key pair rather than each hand-maintaining its own copy that could
    // drift.
    static final String PREFS = "CapacitorStorage";

    // Deliberately NOT the same key queue.ts's own read-modify-write cycle
    // uses (agentmesh.geofence.queue). This process can be torn down the
    // instant onReceive returns, with no coordination possible with the
    // WebView's JS.
    static final String QUEUE_KEY = "agentmesh.geofence.native_queue";

    // Serializes this receiver's append() against GeofencePlugin's
    // drainNativeQueue(). Both run in the app's main process (neither
    // component declares android:process), so a plain in-process monitor is
    // enough -- no cross-process lock needed. Without it, a read-then-write
    // on either side can interleave with the other's write and silently
    // drop a crossing; see drainNativeQueue()'s doc comment for the shape
    // that bug used to take.
    static final Object LOCK = new Object();

    // A per-process monotonic counter, not just the millisecond timestamp
    // below: Android's fused location provider is documented to sometimes
    // hand consecutive geofence transition broadcasts the SAME cached
    // Location object when a fresh GPS fix hasn't landed between them --
    // this is a real platform behavior, not merely "GPS jitter" narrowing
    // with finer clock resolution. An ENTER and an EXIT delivered with a
    // bit-identical Location would still collide on
    // queue.ts's `${workflowId}@${recordedAt}` dedup key even at millisecond
    // precision. Folding this counter into the key (see iso8601's caller)
    // makes every appended fix's key unique regardless of what the location
    // timestamp says, closing the gap the precision change alone left open.
    private static final AtomicLong SEQ = new AtomicLong();

    @Override
    public void onReceive(Context context, Intent intent) {
        GeofencingEvent event = GeofencingEvent.fromIntent(intent);
        if (event == null || event.hasError()) {
            Log.w(TAG, "geofence broadcast carried an error: "
                    + (event == null ? "null event" : String.valueOf(event.getErrorCode())));
            return;
        }

        int transition = event.getGeofenceTransition();
        if (transition != Geofence.GEOFENCE_TRANSITION_ENTER
                && transition != Geofence.GEOFENCE_TRANSITION_EXIT) {
            return;
        }

        Location location = event.getTriggeringLocation();
        List<Geofence> fences = event.getTriggeringGeofences();
        if (location == null || fences == null) {
            return;
        }

        // The fence id IS the workflow id -- that is how GeofencePlugin
        // registers them, so no lookup table is needed to route a crossing
        // back to the workflow it belongs to.
        for (Geofence fence : fences) {
            append(context, fence.getRequestId(), location);
            Log.i(TAG, "queued " + (transition == Geofence.GEOFENCE_TRANSITION_ENTER ? "entry" : "exit")
                    + " for workflow " + fence.getRequestId());
        }
    }

    private void append(Context context, String workflowId, Location location) {
        SharedPreferences prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        synchronized (LOCK) {
            // A corrupt backlog must not cost the crossing that just happened --
            // that would lose the one event this whole receiver exists for, to
            // protect a backlog that is already lost anyway. Falling back to an
            // empty array recovers exactly the way queue.ts's parseFixes() does
            // on the JS side: keep going with a fresh queue rather than refusing
            // to record anything ever again.
            String raw = prefs.getString(QUEUE_KEY, "[]");
            JSONArray queue;
            try {
                queue = new JSONArray(raw == null ? "[]" : raw);
            } catch (JSONException e) {
                Log.e(TAG, "native queue was corrupt, starting a fresh one", e);
                queue = new JSONArray();
            }

            try {
                JSONObject fix = new JSONObject();
                fix.put("workflowId", workflowId);
                fix.put("lat", location.getLatitude());
                fix.put("lng", location.getLongitude());
                if (location.hasAccuracy()) {
                    fix.put("accuracyM", location.getAccuracy());
                }
                // The OS's observation time, not now. The server orders fixes by
                // this and ignores anything older than the last crossing it
                // handled, which is what stops a late flush re-firing a run.
                fix.put("recordedAt", iso8601(location.getTime()));
                // See SEQ's doc comment: disambiguates the dedup key from a
                // same-timestamp sibling, which recordedAt alone cannot always do.
                fix.put("seq", SEQ.getAndIncrement());
                queue.put(fix);

                // commit(), not apply(): this process may be torn down the moment
                // onReceive returns, and an asynchronous write is not guaranteed
                // to have reached disk by then. Losing the one event the whole
                // feature exists for is not an acceptable trade for a few ms.
                prefs.edit().putString(QUEUE_KEY, queue.toString()).commit();
            } catch (Exception e) {
                Log.e(TAG, "could not queue the crossing", e);
            }
        }
    }

    // Millisecond precision, not whole seconds: narrows (but per SEQ's doc
    // comment above, does not by itself eliminate) the window where two
    // fixes queued close together share a dedup key. The backend parses this
    // as RFC3339Nano, which accepts the fractional-seconds component
    // natively -- no backend change needed.
    private String iso8601(long millis) {
        SimpleDateFormat fmt = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US);
        fmt.setTimeZone(TimeZone.getTimeZone("UTC"));
        return fmt.format(new Date(millis));
    }
}
