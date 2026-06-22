package obs

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSchedulerLeaderGaugeAndLog(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	SetSchedulerLeader(true)
	if v := testutil.ToFloat64(SchedulerIsLeader); v != 1 {
		t.Fatalf("gauge after SetSchedulerLeader(true): got %v, want 1", v)
	}
	if !strings.Contains(buf.String(), "became leader") {
		t.Fatalf("log missing 'became leader':\n%s", buf.String())
	}

	buf.Reset()
	SetSchedulerLeader(false)
	if v := testutil.ToFloat64(SchedulerIsLeader); v != 0 {
		t.Fatalf("gauge after SetSchedulerLeader(false): got %v, want 0", v)
	}
	if !strings.Contains(buf.String(), "lost leadership") {
		t.Fatalf("log missing 'lost leadership':\n%s", buf.String())
	}
}
