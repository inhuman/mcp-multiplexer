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
	err := hook(context.Background(), "srv", mcpx.ToolInfo{Name: "del", Destructive: true}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "del")
}

func TestDenyDestructive_Allows(t *testing.T) {
	hook := DenyDestructive()
	err := hook(context.Background(), "srv", mcpx.ToolInfo{Name: "read", Destructive: false}, nil)
	require.NoError(t, err)
}

// --- RequireRoles ---

func TestRequireRoles_Allows(t *testing.T) {
	hook := RequireRoles("admin")
	ctx := context.WithValue(context.Background(), RolesKey, []string{"viewer", "admin"})
	require.NoError(t, hook(ctx, "srv", mcpx.ToolInfo{}, nil))
}

func TestRequireRoles_DeniesWrongRole(t *testing.T) {
	hook := RequireRoles("admin")
	ctx := context.WithValue(context.Background(), RolesKey, []string{"viewer"})
	require.Error(t, hook(ctx, "srv", mcpx.ToolInfo{}, nil))
}

func TestRequireRoles_DeniesNoRolesInCtx(t *testing.T) {
	hook := RequireRoles("admin")
	require.Error(t, hook(context.Background(), "srv", mcpx.ToolInfo{}, nil))
}

func TestRequireRoles_EmptyListDeniesAll(t *testing.T) {
	hook := RequireRoles()
	ctx := context.WithValue(context.Background(), RolesKey, []string{"admin", "superuser"})
	require.Error(t, hook(ctx, "srv", mcpx.ToolInfo{}, nil))
}

// --- RateLimit ---

func TestRateLimit_AllowsBurst(t *testing.T) {
	const burst = 5
	hook := RateLimit(time.Second, burst)
	tool := mcpx.ToolInfo{Name: "tool"}
	for range burst {
		require.NoError(t, hook(context.Background(), "srv", tool, nil))
	}
}

func TestRateLimit_BlocksOverBurst(t *testing.T) {
	const burst = 3
	hook := RateLimit(time.Hour, burst) // very slow refill
	tool := mcpx.ToolInfo{Name: "tool"}
	for range burst {
		require.NoError(t, hook(context.Background(), "srv", tool, nil))
	}
	require.Error(t, hook(context.Background(), "srv", tool, nil))
}

func TestRateLimit_ConcurrentSafe(t *testing.T) {
	hook := RateLimit(time.Millisecond, 100)
	tool := mcpx.ToolInfo{Name: "tool"}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = hook(context.Background(), "srv", tool, nil)
		}()
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
	hook(context.Background(), "srv", mcpx.ToolInfo{Name: "t"}, nil, &mcpx.CallResult{}, nil)
	require.Len(t, logger.infoCalls, 1)
	require.Empty(t, logger.errorCalls)
}

func TestAuditLog_LogsError(t *testing.T) {
	logger := newCaptureLogger()
	hook := AuditLog(logger)
	hook(context.Background(), "srv", mcpx.ToolInfo{Name: "t"}, nil, nil, errors.New("boom"))
	require.Len(t, logger.errorCalls, 1)
	require.Empty(t, logger.infoCalls)
}
