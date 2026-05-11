package policy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// --- DenyDestructive ---

func TestDenyDestructive_Blocks(t *testing.T) {
	hook := DenyDestructive()
	_, _, err := hook(context.Background(), "srv", "del", mcpx.ToolInfo{Name: "del", Destructive: true}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "del")
}

func TestDenyDestructive_Allows(t *testing.T) {
	hook := DenyDestructive()
	_, _, err := hook(context.Background(), "srv", "read", mcpx.ToolInfo{Name: "read", Destructive: false}, nil)
	require.NoError(t, err)
}

// --- RequireRoles ---

func TestRequireRoles_Allows(t *testing.T) {
	hook := RequireRoles("admin")
	ctx := context.WithValue(context.Background(), RolesKey, []string{"viewer", "admin"})
	_, _, err := hook(ctx, "srv", "t", mcpx.ToolInfo{}, nil)
	require.NoError(t, err)
}

func TestRequireRoles_DeniesWrongRole(t *testing.T) {
	hook := RequireRoles("admin")
	ctx := context.WithValue(context.Background(), RolesKey, []string{"viewer"})
	_, _, err := hook(ctx, "srv", "t", mcpx.ToolInfo{}, nil)
	require.Error(t, err)
}

func TestRequireRoles_DeniesNoRolesInCtx(t *testing.T) {
	hook := RequireRoles("admin")
	_, _, err := hook(context.Background(), "srv", "t", mcpx.ToolInfo{}, nil)
	require.Error(t, err)
}

func TestRequireRoles_EmptyListDeniesAll(t *testing.T) {
	hook := RequireRoles()
	ctx := context.WithValue(context.Background(), RolesKey, []string{"admin", "superuser"})
	_, _, err := hook(ctx, "srv", "t", mcpx.ToolInfo{}, nil)
	require.Error(t, err)
}

// --- RateLimit ---

func TestRateLimit_AllowsBurst(t *testing.T) {
	const burst = 5
	hook := RateLimit(time.Second, burst)
	info := mcpx.ToolInfo{Name: "tool"}
	for range burst {
		_, _, err := hook(context.Background(), "srv", "tool", info, nil)
		require.NoError(t, err)
	}
}

func TestRateLimit_BlocksOverBurst(t *testing.T) {
	const burst = 3
	hook := RateLimit(time.Hour, burst) // very slow refill
	info := mcpx.ToolInfo{Name: "tool"}
	for range burst {
		_, _, err := hook(context.Background(), "srv", "tool", info, nil)
		require.NoError(t, err)
	}
	_, _, err := hook(context.Background(), "srv", "tool", info, nil)
	require.Error(t, err)
}

func TestRateLimit_ConcurrentSafe(t *testing.T) {
	hook := RateLimit(time.Millisecond, 100)
	info := mcpx.ToolInfo{Name: "tool"}
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_, _, _ = hook(context.Background(), "srv", "tool", info, nil)
		})
	}
	wg.Wait()
}

// --- AuditLog ---

type captureLogger struct {
	mcpx.Logger
	infoCalls  []string
	errorCalls []string
}

func newCaptureLogger() *captureLogger { return &captureLogger{Logger: mcpx.NopLogger()} }

func (l *captureLogger) Info(msg string, _ ...mcpx.Field)  { l.infoCalls = append(l.infoCalls, msg) }
func (l *captureLogger) Error(msg string, _ ...mcpx.Field) { l.errorCalls = append(l.errorCalls, msg) }

func TestAuditLog_LogsSuccess(t *testing.T) {
	logger := newCaptureLogger()
	hook := AuditLog(logger)
	hook(context.Background(), "srv", "t", mcpx.ToolInfo{Name: "t"}, nil, &mcpx.CallResult{}, nil, 5*time.Millisecond)
	require.Len(t, logger.infoCalls, 1)
	require.Empty(t, logger.errorCalls)
}

func TestAuditLog_LogsError(t *testing.T) {
	logger := newCaptureLogger()
	hook := AuditLog(logger)
	hook(context.Background(), "srv", "t", mcpx.ToolInfo{Name: "t"}, nil, nil, errors.New("boom"), 2*time.Millisecond)
	require.Len(t, logger.errorCalls, 1)
	require.Empty(t, logger.infoCalls)
}
