package ai.agentmesh.app;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import android.content.Context;
import android.content.SharedPreferences;
import android.location.Location;

import androidx.test.core.app.ApplicationProvider;

import org.json.JSONObject;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * GeofenceStore is what BootReceiver trusts to remember every armed fence
 * across a reboot. A regression here does not throw -- it silently drops or
 * corrupts a workflow's fence, and the failure only surfaces as "the trigger
 * stopped firing after the phone restarted", days later and far from this
 * code.
 */
@RunWith(RobolectricTestRunner.class)
@Config(sdk = 34)
public class GeofenceStoreTest {

    private Context context;

    @Before
    public void setUp() {
        context = ApplicationProvider.getApplicationContext();
    }

    @Test
    public void loadAll_onFreshStore_isEmpty() {
        assertTrue(GeofenceStore.loadAll(context).isEmpty());
    }

    @Test
    public void clear_removesEveryFenceBeforeRebootRecovery() {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);
        GeofenceStore.save(context, "workflow-2", -33.9, 151.2, 200.0);
        assertTrue(GeofenceStore.clear(context));
        assertTrue(GeofenceStore.loadAll(context).isEmpty());
        SharedPreferences raw = context.getSharedPreferences("AgentMeshGeofenceStore", Context.MODE_PRIVATE);
        assertTrue(!raw.contains("fences"));
    }

    @Test
    public void deletedFence_cannotQueueALateLocationBroadcast() throws Exception {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);
        GeofenceReceiver receiver = new GeofenceReceiver();
        java.lang.reflect.Method append = GeofenceReceiver.class.getDeclaredMethod(
                "append", Context.class, String.class, Location.class);
        append.setAccessible(true);
        Location location = new Location("test");
        location.setLatitude(12.5);
        location.setLongitude(77.5);
        SharedPreferences queue = context.getSharedPreferences(GeofenceReceiver.PREFS, Context.MODE_PRIVATE);
        append.invoke(receiver, context, "workflow-1", location);
        assertTrue(queue.contains(GeofenceReceiver.QUEUE_KEY));
        queue.edit().remove(GeofenceReceiver.QUEUE_KEY).commit();

        assertTrue(GeofenceStore.clear(context));
        append.invoke(receiver, context, "workflow-1", location);
        assertTrue(!queue.contains(GeofenceReceiver.QUEUE_KEY));
    }

    @Test
    public void save_thenLoadAll_returnsExactlyThatFence() {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);

        List<GeofenceStore.Active> fences = GeofenceStore.loadAll(context);

        assertEquals(1, fences.size());
        GeofenceStore.Active fence = fences.get(0);
        assertEquals("workflow-1", fence.id);
        assertEquals(12.5, fence.lat, 0.0);
        assertEquals(77.5, fence.lng, 0.0);
        assertEquals(150.0, fence.radiusM, 0.0);
    }

    // The reason this store is keyed by id rather than a single slot:
    // GeofencingClient.addGeofences() is additive, and nothing stops a
    // second workflow's fence being armed before the first is disarmed. A
    // regression that went back to a single-slot store would pass every
    // single-fence test and only fail here.
    @Test
    public void save_twoDifferentWorkflows_keepsBothFences() {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);
        GeofenceStore.save(context, "workflow-2", -33.9, 151.2, 200.0);

        List<GeofenceStore.Active> fences = GeofenceStore.loadAll(context);

        assertEquals(2, fences.size());
    }

    @Test
    public void save_sameIdTwice_overwritesRatherThanDuplicates() {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);
        GeofenceStore.save(context, "workflow-1", 40.0, -70.0, 300.0);

        List<GeofenceStore.Active> fences = GeofenceStore.loadAll(context);

        assertEquals(1, fences.size());
        assertEquals(40.0, fences.get(0).lat, 0.0);
    }

    @Test
    public void remove_takesOnlyThatWorkflowsFence() {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);
        GeofenceStore.save(context, "workflow-2", -33.9, 151.2, 200.0);

        GeofenceStore.remove(context, "workflow-1");

        List<GeofenceStore.Active> fences = GeofenceStore.loadAll(context);
        assertEquals(1, fences.size());
        assertEquals("workflow-2", fences.get(0).id);
    }

    @Test
    public void remove_unknownId_isANoOp() {
        GeofenceStore.save(context, "workflow-1", 12.5, 77.5, 150.0);

        GeofenceStore.remove(context, "does-not-exist");

        assertEquals(1, GeofenceStore.loadAll(context).size());
    }

    // loadAll() must survive a hand-corrupted or partially-written prefs
    // file rather than throwing -- BootReceiver calls this with no try/catch
    // of its own, and one bad entry taking the whole reboot-recovery path
    // down would silently kill every OTHER workflow's fence too.
    @Test
    public void loadAll_withCorruptJson_returnsEmptyRatherThanThrowing() {
        SharedPreferences raw = context.getSharedPreferences("AgentMeshGeofenceStore", Context.MODE_PRIVATE);
        raw.edit().putString("fences", "{not valid json").commit();

        assertTrue(GeofenceStore.loadAll(context).isEmpty());
    }

    // Same corruption tolerance, but for a single entry inside an otherwise
    // well-formed object: one workflow's malformed fence must not take the
    // others down with it.
    @Test
    public void loadAll_withOneCorruptEntry_recoversTheRest() throws Exception {
        SharedPreferences raw = context.getSharedPreferences("AgentMeshGeofenceStore", Context.MODE_PRIVATE);
        JSONObject fences = new JSONObject();
        fences.put("workflow-1", new JSONObject().put("lat", 12.5).put("lng", 77.5).put("radiusM", 150.0));
        // Missing radiusM -- getDouble("radiusM") throws JSONException for
        // this one entry only.
        fences.put("workflow-2", new JSONObject().put("lat", 1.0).put("lng", 2.0));
        raw.edit().putString("fences", fences.toString()).commit();

        List<GeofenceStore.Active> loaded = GeofenceStore.loadAll(context);

        assertEquals(1, loaded.size());
        assertEquals("workflow-1", loaded.get(0).id);
    }

    // save()/remove() are called from async GMS callbacks that can land
    // back-to-back for different workflows, plus BootReceiver.loadAll()
    // concurrently. The documented failure mode without LOCK: two threads
    // read the same starting JSONObject, and the second writer's commit()
    // wins outright, dropping whichever fence lost the race. This drives
    // enough concurrent saves that a regression back to unsynchronized
    // read-modify-write reliably loses at least one.
    @Test
    public void concurrentSaves_forDistinctWorkflows_loseNone() throws InterruptedException {
        int threads = 20;
        CountDownLatch ready = new CountDownLatch(threads);
        CountDownLatch go = new CountDownLatch(1);
        CountDownLatch done = new CountDownLatch(threads);
        AtomicInteger failures = new AtomicInteger();

        for (int i = 0; i < threads; i++) {
            final int id = i;
            Thread t = new Thread(() -> {
                ready.countDown();
                try {
                    go.await();
                    GeofenceStore.save(context, "workflow-" + id, id, id, 100.0);
                } catch (InterruptedException e) {
                    failures.incrementAndGet();
                } finally {
                    done.countDown();
                }
            });
            t.start();
        }

        ready.await(5, TimeUnit.SECONDS);
        go.countDown();
        assertTrue(done.await(10, TimeUnit.SECONDS));
        assertEquals(0, failures.get());
        assertEquals(threads, GeofenceStore.loadAll(context).size());
    }
}
