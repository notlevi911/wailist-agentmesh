package ai.agentmesh.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.util.Log;

import com.google.android.gms.location.GeofencingClient;
import com.google.android.gms.location.LocationServices;

import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * GeofencingClient does not survive a reboot -- the OS drops every
 * app-registered fence silently, with no error delivered anywhere. This
 * re-registers every fence GeofenceStore remembers, built the same way
 * GeofencePlugin.addGeofence originally built it, so a trigger a user
 * configured keeps firing after the phone restarts instead of going quietly
 * dead until they happen to reopen the app and re-save it.
 */
public class BootReceiver extends BroadcastReceiver {

    private static final String TAG = "AgentMeshGeofenceBoot";

    @Override
    public void onReceive(Context context, Intent intent) {
        if (!Intent.ACTION_BOOT_COMPLETED.equals(intent.getAction())) {
            return;
        }
        List<GeofenceStore.Active> fences = GeofenceStore.loadAll(context.getApplicationContext());
        if (fences.isEmpty()) {
            return;
        }

        // addGeofences() is asynchronous, and the system reclaims process
        // priority the moment onReceive() returns -- without goAsync() the
        // process can be killed before any of these calls complete, and the
        // one feature this receiver exists for would fail silently at
        // exactly the moment it matters most. finish() is called once every
        // outstanding registration has reported back, success or failure.
        PendingResult pending = goAsync();
        AtomicInteger outstanding = new AtomicInteger(fences.size());
        Context appContext = context.getApplicationContext();
        GeofencingClient client = LocationServices.getGeofencingClient(appContext);

        for (GeofenceStore.Active active : fences) {
            GeofenceRegistrar.register(appContext, client, active.id, active.lat, active.lng, active.radiusM,
                    (success, error) -> {
                        if (success) {
                            Log.i(TAG, "re-registered geofence for workflow " + active.id + " after reboot");
                        } else {
                            Log.e(TAG, "could not re-register geofence for workflow " + active.id
                                    + " after reboot: " + error);
                        }
                        if (outstanding.decrementAndGet() == 0) {
                            pending.finish();
                        }
                    });
        }
    }
}
