package obs

import "log/slog"

// SetSchedulerLeader updates the hookrail_scheduler_is_leader gauge and logs
// the transition. Called by the Elector's isLeader callback.
func SetSchedulerLeader(v bool) {
	if v {
		SchedulerIsLeader.Set(1)
		slog.Info("became leader")
	} else {
		SchedulerIsLeader.Set(0)
		slog.Info("lost leadership")
	}
}
