package messaging

// Lifecycle receives adapter-owned startup facts. Callbacks are optional.
// Starting is reported before each launch attempt; Running is reported only
// after the adapter's external delivery boundary is ready.
type Lifecycle struct {
	Starting func()
	Running  func()
}

func (l Lifecycle) ReportStarting() {
	if l.Starting != nil {
		l.Starting()
	}
}

func (l Lifecycle) ReportRunning() {
	if l.Running != nil {
		l.Running()
	}
}
