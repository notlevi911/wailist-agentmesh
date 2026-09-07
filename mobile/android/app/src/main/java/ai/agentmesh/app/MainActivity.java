package ai.agentmesh.app;

import android.os.Bundle;

import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        // Capacitor auto-discovers plugins shipped as packages; one that lives
        // in the app module has to be registered explicitly, and before
        // super.onCreate, or the bridge is built without it.
        registerPlugin(GeofencePlugin.class);
        super.onCreate(savedInstanceState);
    }
}
