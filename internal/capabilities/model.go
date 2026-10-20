package capabilities

import "os"

// probeModelPresent auto-detects whether a verified inference model is present
// on the host. Like the share-mount probe it is a host fact
// surfaced through an env attestation (BEAMFALL_INFERENCE_MODEL_PRESENT=1) so
// the capability layer can report model presence without acquiring a new
// external surface or reaching into the Integration Plugin's model store
// (Safety Corollary). An unset/empty value means no model — the default on a
// stock binary — which degrades the capability to
// `enabled:false, reason:model-absent` (a discoverable "download a model to
// enable" state, never a silent dead end).
func probeModelPresent() bool {
	return os.Getenv("BEAMFALL_INFERENCE_MODEL_PRESENT") == "1"
}
