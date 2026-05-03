package page

// LifecycleEventName is kept as a local compatibility overlay.
// The upstream protocol models Page.lifecycleEvent.name as a bare string,
// but the high-level page layer relies on stable named constants.
type LifecycleEventName = string

const (
	LifecycleEventDOMContentLoaded LifecycleEventName = "DOMContentLoaded"
	LifecycleEventLoad             LifecycleEventName = "load"
	LifecycleEventNetworkIdle      LifecycleEventName = "networkIdle"
)
