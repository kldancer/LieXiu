package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisClientName(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		suffix   string
		want     string
	}{
		{"empty_suffix_returns_existing", "liexiu-api:store", "", "liexiu-api:store"},
		{"empty_existing_uses_default_prefix", "", "store", "liexiu-api:store"},
		{"both_set_joins_with_colon", "custom", "store", "custom:store"},
		{"empty_both_returns_empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redisClientName(tt.existing, tt.suffix)
			if got != tt.want {
				t.Errorf("redisClientName(%q, %q) = %q, want %q", tt.existing, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestNewNamedRedisClient_SetsClientName(t *testing.T) {
	t.Setenv("REDIS_DISABLE_CLIENT_NAME", "")
	base := &redis.Options{Addr: "localhost:6379"}
	client := newNamedRedisClient(base, "store")
	defer client.Close()

	opts := client.Options()
	if opts.ClientName != "liexiu-api:store" {
		t.Errorf("ClientName = %q, want %q", opts.ClientName, "liexiu-api:store")
	}
}

func TestNewNamedRedisClient_DisableClientName(t *testing.T) {
	t.Setenv("REDIS_DISABLE_CLIENT_NAME", "true")
	base := &redis.Options{Addr: "localhost:6379"}
	client := newNamedRedisClient(base, "store")
	defer client.Close()

	opts := client.Options()
	if opts.ClientName != "" {
		t.Errorf("ClientName = %q, want empty when REDIS_DISABLE_CLIENT_NAME=true", opts.ClientName)
	}
}

func TestNewNamedRedisClient_DisableClientName_ClearsPreExistingName(t *testing.T) {
	t.Setenv("REDIS_DISABLE_CLIENT_NAME", "true")
	// Simulate REDIS_URL with ?client_name=foo — ParseURL sets ClientName.
	base := &redis.Options{Addr: "localhost:6379", ClientName: "foo"}
	client := newNamedRedisClient(base, "store")
	defer client.Close()

	opts := client.Options()
	if opts.ClientName != "" {
		t.Errorf("ClientName = %q, want empty: REDIS_DISABLE_CLIENT_NAME must clear pre-existing name from URL", opts.ClientName)
	}
}

func TestNewNamedRedisClient_DisableClientName_InvalidValue(t *testing.T) {
	t.Setenv("REDIS_DISABLE_CLIENT_NAME", "not-a-bool")
	base := &redis.Options{Addr: "localhost:6379"}
	client := newNamedRedisClient(base, "store")
	defer client.Close()

	opts := client.Options()
	// Invalid value falls back to default (false), so ClientName IS set
	if opts.ClientName != "liexiu-api:store" {
		t.Errorf("ClientName = %q, want %q (invalid env should fall back to naming enabled)", opts.ClientName, "liexiu-api:store")
	}
}

// mainSourceFile is parsed by TestMainUsesRouterOwnedBackgroundServices. The
// test asserts the markers below are actually present so a future move of the
// background-worker wiring fails loudly instead of vacuously passing on a walk
// that matched nothing.
const mainSourceFile = "main.go"

// backgroundServiceConstructors must never be called from main(): they build a
// TaskService that the router has not finished wiring.
var backgroundServiceConstructors = []string{
	"service.NewTaskService",
}

// TestMainUsesRouterOwnedBackgroundServices guards the process wiring behind
// the runtime sweeper: background work must take the router's fully wired
// services off *handler.Handler instead of constructing its own.
//
// The router — not NewTaskService — assigns h.TaskService.EmptyClaim. A second
// TaskService built inside main() therefore has EmptyClaim == nil, and because
// EmptyClaimCache is deliberately nil-safe the missed invalidation is silent:
// a scheduled dispatch still delivers the daemon wakeup while the claim path's
// cached "no queued task" verdict survives, so an idle runtime keeps returning
// empty claims until EmptyClaimCacheTTL expires.
//
// This parses main.go instead of asserting on backgroundTaskService's return
// values. A value-level assertion only proves the helper hands back h's fields,
// which stays true even when main() stops calling it and constructs its own
// services again — i.e. it cannot fail on the exact regression it names.
func TestMainUsesRouterOwnedBackgroundServices(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainSourceFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", mainSourceFile, err)
	}

	var mainFunc *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFunc = fn
			break
		}
	}
	if mainFunc == nil || mainFunc.Body == nil {
		t.Fatalf("no func main() with a body found in %s — this guard must be pointed at the real wiring", mainSourceFile)
	}

	// Resolve the variable holding the router's *handler.Handler rather than
	// hardcoding "h": the argument identity is the whole point of the guard,
	// and reading it off the NewRouterWithOptions assignment keeps a rename of
	// that variable from silently weakening the check below.
	routerHandlerVar := routerHandlerIdent(mainFunc.Body)
	if routerHandlerVar == "" {
		t.Fatalf("could not find the *handler.Handler result of NewRouterWithOptions in main() — re-point this guard at the current router wiring")
	}

	forbidden := make(map[string]bool, len(backgroundServiceConstructors))
	for _, name := range backgroundServiceConstructors {
		forbidden[name] = true
	}

	var (
		offenders    []string
		badReuse     []string
		reusesRouter bool
	)
	ast.Inspect(mainFunc.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callee := calleeName(call); {
		case forbidden[callee]:
			offenders = append(offenders, fmt.Sprintf("%s at %s", callee, fset.Position(call.Pos())))
		case callee == "backgroundTaskService":
			// Matching the callee name alone would accept
			// backgroundTaskService(nil) — which compiles, reuses nothing, and
			// panics at startup. The argument must be the router's handler.
			if len(call.Args) == 1 {
				if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == routerHandlerVar {
					reusesRouter = true
					return true
				}
			}
			badReuse = append(badReuse, fmt.Sprintf("%s at %s", exprText(fset, call), fset.Position(call.Pos())))
		}
		return true
	})

	if len(offenders) > 0 {
		t.Errorf("main() constructs its own background services (%s); take them from the router via backgroundTaskService(%s) so EmptyClaim and the rest of the router wiring come along", strings.Join(offenders, ", "), routerHandlerVar)
	}
	if len(badReuse) > 0 {
		t.Errorf("main() calls backgroundTaskService with something other than the router handler %q (%s); background workers must reuse the services the router finished wiring", routerHandlerVar, strings.Join(badReuse, ", "))
	}
	if !reusesRouter {
		t.Errorf("main() no longer calls backgroundTaskService(%s); background workers must reuse the router-owned TaskService", routerHandlerVar)
	}
}

func TestPersonalAutoLoginEnabled(t *testing.T) {
	for _, test := range []struct {
		name   string
		appEnv string
		raw    string
		want   bool
	}{
		{name: "local explicit", raw: "true", want: true},
		{name: "trimmed", appEnv: "development", raw: " TRUE ", want: true},
		{name: "off by default", raw: "", want: false},
		{name: "explicit false", raw: "false", want: false},
		{name: "production forced off", appEnv: "production", raw: "true", want: false},
		{name: "production case insensitive", appEnv: " Production ", raw: "TRUE", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := personalAutoLoginEnabled(test.appEnv, test.raw); got != test.want {
				t.Fatalf("personalAutoLoginEnabled(%q, %q) = %v, want %v", test.appEnv, test.raw, got, test.want)
			}
		})
	}
}

func TestMainStartsRunReconcilerWithBackgroundLifecycle(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainSourceFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", mainSourceFile, err)
	}
	var mainFunc *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFunc = fn
			break
		}
	}
	if mainFunc == nil || mainFunc.Body == nil {
		t.Fatalf("no func main() with a body found in %s", mainSourceFile)
	}

	var reconcilerVar string
	ast.Inspect(mainFunc.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || calleeName(call) != "h.Orchestration.NewRunReconciler" {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok {
			reconcilerVar = ident.Name
		}
		return true
	})
	if reconcilerVar == "" {
		t.Fatal("main() no longer constructs the router-owned orchestration RunReconciler")
	}

	started := false
	ast.Inspect(mainFunc.Body, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok || calleeName(goStmt.Call) != reconcilerVar+".Run" || len(goStmt.Call.Args) != 1 {
			return true
		}
		ctx, ok := goStmt.Call.Args[0].(*ast.Ident)
		started = ok && ctx.Name == "sweepCtx"
		return true
	})
	if !started {
		t.Fatalf("main() does not run %s.Run(sweepCtx); startup recovery must share the server background lifecycle", reconcilerVar)
	}
}

// routerHandlerIdent returns the name of the variable that receives the
// *handler.Handler from NewRouterWithOptions (the `h` in `r, h := ...`), or ""
// when that assignment is no longer recognizable.
func routerHandlerIdent(body *ast.BlockStmt) string {
	var name string
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 2 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || calleeName(call) != "NewRouterWithOptions" {
			return true
		}
		if ident, ok := assign.Lhs[1].(*ast.Ident); ok {
			name = ident.Name
			return false
		}
		return true
	})
	return name
}

// exprText renders an expression back to source for error messages.
func exprText(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "<unprintable expression>"
	}
	return buf.String()
}

// calleeName renders a call's target as "pkg.Func" or "Func" for matching.
func calleeName(call *ast.CallExpr) string {
	return expressionName(call.Fun)
}

func expressionName(expression ast.Expr) string {
	switch fn := expression.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		prefix := expressionName(fn.X)
		if prefix != "" {
			return prefix + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	}
	return ""
}

// TestNormalizeServerVersion covers the router-config wiring path (not just
// a hand-set handler.Config field): an unstamped "dev" build must not leak
// into /api/config's server_version, or the Help popover would render
// "Server version dev" instead of hiding the row.
func TestNormalizeServerVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unstamped_dev_default_becomes_empty", "dev", ""},
		{"already_empty_stays_empty", "", ""},
		{"stamped_release_tag_passes_through", "v0.4.0", "v0.4.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeServerVersion(tt.in); got != tt.want {
				t.Errorf("normalizeServerVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		def   bool
		want  bool
	}{
		{"empty_returns_default_false", "TEST_ENV_BOOL_1", "", false, false},
		{"empty_returns_default_true", "TEST_ENV_BOOL_2", "", true, true},
		{"true_string", "TEST_ENV_BOOL_3", "true", false, true},
		{"false_string", "TEST_ENV_BOOL_4", "false", true, false},
		{"one_is_true", "TEST_ENV_BOOL_5", "1", false, true},
		{"zero_is_false", "TEST_ENV_BOOL_6", "0", true, false},
		{"invalid_returns_default", "TEST_ENV_BOOL_7", "maybe", false, false},
		{"invalid_returns_default_true", "TEST_ENV_BOOL_8", "maybe", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != "" {
				t.Setenv(tt.key, tt.value)
			} else {
				os.Unsetenv(tt.key)
			}
			got := envBool(tt.key, tt.def)
			if got != tt.want {
				t.Errorf("envBool(%q, %v) = %v, want %v", tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestEnvNonNegativeDuration(t *testing.T) {
	tests := []struct {
		name  string
		value string
		def   time.Duration
		want  time.Duration
	}{
		{name: "unset returns default", def: 3 * time.Second, want: 3 * time.Second},
		{name: "empty returns default", value: "", def: 2 * time.Second, want: 2 * time.Second},
		{name: "bare zero disables hold", value: "0", def: time.Second, want: 0},
		{name: "zero duration disables hold", value: "0s", def: time.Second, want: 0},
		{name: "positive duration", value: "5m", want: 5 * time.Minute},
		{name: "invalid returns default", value: "later", def: 4 * time.Second, want: 4 * time.Second},
		{name: "negative returns default", value: "-1s", def: 4 * time.Second, want: 4 * time.Second},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_NON_NEGATIVE_DURATION_" + strconv.Itoa(i)
			if tt.name == "unset returns default" {
				os.Unsetenv(key)
			} else {
				t.Setenv(key, tt.value)
			}
			if got := envNonNegativeDuration(key, tt.def); got != tt.want {
				t.Fatalf("envNonNegativeDuration(%q, %s) = %s, want %s", key, tt.def, got, tt.want)
			}
		})
	}
}

func TestValidateStartupSecurity(t *testing.T) {
	tests := []struct {
		name    string
		appEnv  string
		secret  string
		wantErr bool
	}{
		{name: "production rejects empty secret", appEnv: "production", wantErr: true},
		{name: "production rejects whitespace secret", appEnv: " production ", secret: "  \t", wantErr: true},
		{name: "production accepts configured secret", appEnv: "PRODUCTION", secret: "a-secret", wantErr: false},
		{name: "development preserves empty secret compatibility", appEnv: "development", wantErr: false},
		{name: "unset environment preserves empty secret compatibility", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStartupSecurity(tt.appEnv, tt.secret)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateStartupSecurity(%q, %q) error = %v, wantErr %v", tt.appEnv, tt.secret, err, tt.wantErr)
			}
		})
	}
}

func TestHoldBeforeShutdown(t *testing.T) {
	const hold = 10 * time.Millisecond
	started := time.Now()
	holdBeforeShutdown(syscall.SIGTERM, nil, hold)
	if elapsed := time.Since(started); elapsed < hold {
		t.Fatalf("holdBeforeShutdown returned after %s, before configured hold %s", elapsed, hold)
	}
}

func TestHoldBeforeShutdownInterruptedBySecondSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGINT
	done := make(chan struct{})

	go func() {
		holdBeforeShutdown(syscall.SIGTERM, signals, time.Minute)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("holdBeforeShutdown did not return after a second signal")
	}
	if len(signals) != 0 {
		t.Fatal("holdBeforeShutdown did not consume the second signal")
	}
}

func TestHoldBeforeShutdownDisabled(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGINT
	holdBeforeShutdown(syscall.SIGTERM, signals, 0)
	if len(signals) != 1 {
		t.Fatal("disabled hold should not consume another signal")
	}
}
