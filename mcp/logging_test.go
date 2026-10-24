package mcp

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestSlogLevelToMCPCustomLevels(t *testing.T) {
	for _, tt := range []struct {
		level slog.Level
		want  LoggingLevel
	}{
		{slog.LevelDebug - 4, "debug"},
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo - 1, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelInfo + 1, "info"},
		{LevelNotice, "notice"},
		{slog.LevelWarn + 1, "warning"},
		{slog.LevelError + 2, "error"},
		{LevelEmergency + 10, "emergency"},
	} {
		if got := slogLevelToMCP(tt.level); got != tt.want {
			t.Errorf("slogLevelToMCP(%v) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// A message logged at a custom slog level that LoggingHandler.Enabled
// accepts must reach the client, with the nearest MCP level.
func TestLoggingHandlerCustomLevel(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()
	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *LoggingMessageParams, 10)
	c := NewClient(testImpl, &ClientOptions{
		LoggingMessageHandler: func(_ context.Context, r *LoggingMessageRequest) { got <- r.Params },
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if err := cs.SetLoggingLevel(ctx, &SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(NewLoggingHandler(ss, nil))
	lvl := slog.LevelInfo + 1
	if !logger.Enabled(ctx, lvl) {
		t.Fatalf("Enabled(%v) = false, want true", lvl)
	}
	logger.Log(ctx, lvl, "hello")
	select {
	case p := <-got:
		if p.Level != "info" {
			t.Errorf("got level %q, want %q", p.Level, "info")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("message at level %v was dropped", lvl)
	}
}
