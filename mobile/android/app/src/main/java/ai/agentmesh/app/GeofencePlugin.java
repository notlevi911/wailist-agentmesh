package ai.agentmesh.app;

import android.Manifest;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.Uri;
import android.provider.Settings;
import android.content.pm.PackageManager;
import android.os.Build;

import androidx.core.content.ContextCompat;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;
import com.google.android.gms.location.GeofencingClient;
import com.google.android.gms.location.LocationServices;

import java.util.Collections;

/**
 * Geofencing on Android's own GeofencingClient.
 *
 * This exists as the free alternative to a commercial background-tracking SDK.
 * The distinction that makes it viable: we do not want continuous background
 * TRACKING, only "did this device cross the edge of one circle". That is
 * precisely what the platform API does, it is free, and the OS batches the
 * work across every app on the device -- which is why it costs far less
 * battery than any polling loop could.
 *
 * What it deliberately does not do is deliver the crossing to the server from
 * here. See GeofenceReceiver for why, and for where that boundary sits.
 */
@CapacitorPlugin(
    name = "Geofence",
    permissions = {
        // Two aliases, not one, because Android grants them in two steps.
        // Asking for background in the same breath as foreground gets the
        // whole request denied on Android 11+, so they are requested in
        // sequence and reported separately.
        @Permission(alias = "location", strings = { Manifest.permission.ACCESS_FINE_LOCATION }),
        @Permission(alias = "background", strings = { Manifest.permission.ACCESS_BACKGROUND_LOCATION })
    }
)
public class GeofencePlugin extends Plugin {

    private GeofencingClient client;

    @Override
    public void load() {
        client = LocationServices.getGeofencingClient(getContext());
    }

    /**
     * Background location is a separate, later grant than foreground on
     * Android 10+, and a geofence that only holds while the app is open is
     * not a geofence anyone wants. Reported rather than silently degraded:
     * "it works until you pocket the phone" is the worst possible outcome.
     */
    private boolean hasBackgroundLocation() {
        if (ContextCompat.checkSelfPermission(getContext(), Manifest.permission.ACCESS_FINE_LOCATION)
                != PackageManager.PERMISSION_GRANTED) {
            return false;
        }
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            return true;
        }
        return ContextCompat.checkSelfPermission(getContext(), Manifest.permission.ACCESS_BACKGROUND_LOCATION)
                == PackageManager.PERMISSION_GRANTED;
    }

    /**
     * Requests foreground location, then background.
     *
     * Sequenced deliberately. From Android 11 a request that asks for
     * background alongside foreground is denied outright without ever showing
     * the user a dialog -- the platform requires foreground to already be held
     * before background can even be asked for.
     */
    @PluginMethod
    public void requestPermission(PluginCall call) {
        if (ContextCompat.checkSelfPermission(getContext(), Manifest.permission.ACCESS_FINE_LOCATION)
                != PackageManager.PERMISSION_GRANTED) {
            requestPermissionForAlias("location", call, "afterForeground");
            return;
        }
        afterForeground(call);
    }

    @PermissionCallback
    private void afterForeground(PluginCall call) {
        if (ContextCompat.checkSelfPermission(getContext(), Manifest.permission.ACCESS_FINE_LOCATION)
                != PackageManager.PERMISSION_GRANTED) {
            JSObject out = new JSObject();
            out.put("granted", false);
            out.put("reason", "foreground");
            call.resolve(out);
            return;
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                && ContextCompat.checkSelfPermission(getContext(), Manifest.permission.ACCESS_BACKGROUND_LOCATION)
                != PackageManager.PERMISSION_GRANTED) {
            requestPermissionForAlias("background", call, "afterBackground");
            return;
        }
        afterBackground(call);
    }

    @PermissionCallback
    private void afterBackground(PluginCall call) {
        JSObject out = new JSObject();
        boolean granted = hasBackgroundLocation();
        out.put("granted", granted);
        if (!granted) out.put("reason", "background");
        call.resolve(out);
    }

    /**
     * Opens this app's settings page.
     *
     * The only route back after a refusal: Android will not show the
     * permission dialog again once someone has declined it, so an app that
     * cannot send them here has no way to recover except being reinstalled.
     */
    @PluginMethod
    public void openSettings(PluginCall call) {
        Intent intent = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                Uri.fromParts("package", getContext().getPackageName(), null));
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        getContext().startActivity(intent);
        call.resolve();
    }

    @PluginMethod
    public void hasPermission(PluginCall call) {
        JSObject out = new JSObject();
        out.put("granted", hasBackgroundLocation());
        call.resolve(out);
    }

    @PluginMethod
    public void addGeofence(PluginCall call) {
        String id = call.getString("id");
        Double lat = call.getDouble("lat");
        Double lng = call.getDouble("lng");
        Double radius = call.getDouble("radiusM");

        if (id == null || lat == null || lng == null || radius == null) {
            call.reject("id, lat, lng and radiusM are all required");
            return;
        }
        if (!hasBackgroundLocation()) {
            call.reject("background location permission has not been granted");
            return;
        }

        // Shared with BootReceiver so a boot-time re-registration builds the
        // exact same request this live call does.
        GeofenceRegistrar.register(getContext(), client, id, lat, lng, radius, (success, error) -> {
            if (!success) {
                call.reject("could not register the geofence: " + error);
                return;
            }
            // Persisted so BootReceiver can re-register this fence:
            // GeofencingClient does not survive a reboot on its own, and
            // BootReceiver has no WebView to ask for these values the way
            // this call did.
            GeofenceStore.save(getContext(), id, lat, lng, radius);
            call.resolve();
        });
    }

    @PluginMethod
    public void removeGeofence(PluginCall call) {
        String id = call.getString("id");
        if (id == null) {
            call.reject("id is required");
            return;
        }
        client.removeGeofences(Collections.singletonList(id))
                .addOnSuccessListener(unused -> {
                    GeofenceStore.remove(getContext(), id);
                    call.resolve();
                })
                .addOnFailureListener(e -> call.reject("could not remove the geofence: " + e.getMessage()));
    }

    /**
     * Atomically reads and clears GeofenceReceiver's queue in one native call.
     *
     * queue.ts used to do this with two separate @capacitor/preferences calls
     * (a read, then a write of whatever was left over) with no way to
     * coordinate against GeofenceReceiver.append() running in between --
     * append() could land after the second read but before the write landed,
     * and that crossing would be silently overwritten. Doing the read and the
     * clear inside one synchronized(LOCK) block, in the same call
     * GeofenceReceiver.append() itself synchronizes on, makes the two
     * mutually exclusive instead of racing.
     */
    @PluginMethod
    public void drainNativeQueue(PluginCall call) {
        SharedPreferences prefs = getContext().getSharedPreferences(GeofenceReceiver.PREFS, Context.MODE_PRIVATE);
        String raw;
        synchronized (GeofenceReceiver.LOCK) {
            raw = prefs.getString(GeofenceReceiver.QUEUE_KEY, "[]");
            prefs.edit().remove(GeofenceReceiver.QUEUE_KEY).commit();
        }
        JSObject out = new JSObject();
        out.put("value", raw == null ? "[]" : raw);
        call.resolve(out);
    }
}
