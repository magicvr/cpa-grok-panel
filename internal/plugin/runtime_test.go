package plugin

import (
	"testing"
)

func TestShutdownStopsAllWorkers(t *testing.T) {
	runtime := NewRuntime(nil)
	if err := runtime.ensureReady(t.TempDir()); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}
	if runtime.worker == nil || runtime.probeWorker == nil || runtime.usageResetWorker == nil {
		t.Fatalf("workers not started: demotion=%v probe=%v usageReset=%v",
			runtime.worker, runtime.probeWorker, runtime.usageResetWorker)
	}
	if !runtime.ready {
		t.Fatal("expected ready after ensureReady")
	}

	if err := runtime.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if runtime.worker != nil {
		t.Fatal("demotion worker not cleared")
	}
	if runtime.probeWorker != nil {
		t.Fatal("probe worker not cleared")
	}
	if runtime.usageResetWorker != nil {
		t.Fatal("usageReset worker not cleared")
	}
	if runtime.store != nil || runtime.usage != nil || runtime.router != nil {
		t.Fatal("service pointers not cleared")
	}
	if runtime.ready {
		t.Fatal("ready still true after Shutdown")
	}

	// Second Shutdown must be a no-op (idempotent).
	if err := runtime.Shutdown(); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}
