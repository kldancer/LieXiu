package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/kailonyang/liexiu/server/internal/daemonws"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/handler"
	"github.com/kailonyang/liexiu/server/internal/logger"
	obsmetrics "github.com/kailonyang/liexiu/server/internal/metrics"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	"github.com/kailonyang/liexiu/server/internal/scheduler"
	"github.com/kailonyang/liexiu/server/internal/service"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/featureflag"
	"github.com/redis/go-redis/v9"
)

var (
	version = "dev"
	commit  = "unknown"
)

func newNamedRedisClient(base *redis.Options, suffix string) *redis.Client {
	opts := *base
	if envBool("REDIS_DISABLE_CLIENT_NAME", false) {
		opts.ClientName = ""
	} else {
		opts.ClientName = redisClientName(opts.ClientName, suffix)
	}
	return redis.NewClient(&opts)
}

func redisClientName(existing, suffix string) string {
	if suffix == "" {
		return existing
	}
	if existing != "" {
		return existing + ":" + suffix
	}
	return "liexiu-api:" + suffix
}

func closeRedisClient(label string, client *redis.Client) {
	if client == nil {
		return
	}
	if err := client.Close(); err != nil {
		slog.Warn("redis client close failed", "client", label, "error", err)
	}
}

func shardedRelayConfigFromEnv() realtime.ShardedStreamRelayConfig {
	cfg := realtime.DefaultShardedStreamRelayConfig()
	cfg.Shards = envPositiveInt("REALTIME_RELAY_SHARDS", cfg.Shards)
	cfg.StreamMaxLen = envPositiveInt64("REALTIME_RELAY_STREAM_MAXLEN", cfg.StreamMaxLen)
	cfg.ReadCount = envPositiveInt64("REALTIME_RELAY_XREAD_COUNT", cfg.ReadCount)
	cfg.ReadBlock = envDuration("REALTIME_RELAY_XREAD_BLOCK", cfg.ReadBlock)
	cfg.ReplayGrace = envDuration("REALTIME_RELAY_REPLAY_GRACE", cfg.ReplayGrace)
	return cfg
}

func realtimeRelayModeFromEnv() string {
	const defaultMode = "sharded"
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("REALTIME_RELAY_MODE")))
	if raw == "" {
		return defaultMode
	}
	switch raw {
	case "sharded", "dual", "legacy":
		return raw
	default:
		slog.Warn("invalid env var, using default", "name", "REALTIME_RELAY_MODE", "value", raw, "default", defaultMode)
		return defaultMode
	}
}

func envPositiveInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func envPositiveInt64(name string, def int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

func envDuration(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return v
}

func envNonNegativeDuration(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < 0 {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return v
}

func holdBeforeShutdown(sig os.Signal, signals <-chan os.Signal, duration time.Duration) {
	if duration <= 0 {
		return
	}
	slog.Info("termination signal received; holding before shutdown",
		"signal", sig.String(),
		"duration", duration.String(),
	)
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		slog.Info("shutdown hold complete", "duration", duration.String())
	case interruptSig := <-signals:
		slog.Info("shutdown hold interrupted by signal",
			"signal", interruptSig.String(),
			"configured_duration", duration.String(),
		)
	}
}

func envBool(name string, def bool) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		slog.Warn("invalid env var, using default", "name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return v
}

// validateStartupSecurity enforces the process-level secret contract before
// any auth-dependent server wiring can use the development fallback.
func validateStartupSecurity(appEnv, jwtSecret string) error {
	if strings.EqualFold(strings.TrimSpace(appEnv), "production") && strings.TrimSpace(jwtSecret) == "" {
		return fmt.Errorf("JWT_SECRET must be set when APP_ENV=production")
	}
	return nil
}

func backgroundTaskService(h *handler.Handler) *service.TaskService {
	return h.TaskService
}

func main() {
	logger.Init()

	// Refuse to start production with the auth package's development fallback.
	jwtSecret := os.Getenv("JWT_SECRET")
	if err := validateStartupSecurity(os.Getenv("APP_ENV"), jwtSecret); err != nil {
		slog.Error("startup security validation failed", "error", err)
		os.Exit(1)
	}
	// Warn about missing configuration in development, preserving the local
	// compatibility behavior where auth uses its development fallback.
	if strings.TrimSpace(jwtSecret) == "" {
		slog.Warn("JWT_SECRET is not set — using insecure default. Set JWT_SECRET for production use.")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	shutdownHoldDuration := envNonNegativeDuration("LIEXIU_SHUTDOWN_HOLD_DURATION", 0)

	// Feature flags: loaded once at startup from LIEXIU_FEATURE_FLAGS_FILE
	// (a YAML rule set) with FF_<KEY> env overrides layered on top.
	// See server/pkg/featureflag for the schema and lifecycle rules.
	//
	// Booting the server without any flag config is intentional: when the
	// env var is unset, every IsEnabled call falls through to the caller's
	// default, so existing code paths are unchanged until someone adds a
	// rule. A misconfigured (malformed / missing) file surfaces as a hard
	// error so operators see misconfig the same way they do for any other
	// LIEXIU_*_FILE knob.
	flags, err := featureflag.NewServiceFromEnv(featureflag.WithLogger(slog.Default()))
	if err != nil {
		slog.Error("feature flag configuration failed to load", "error", err)
		os.Exit(1)
	}
	_ = flags // adopted by the router (opts.FeatureFlags) and server-side toggle points; see server/pkg/featureflag

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://liexiu:liexiu@localhost:5432/liexiu?sslmode=disable"
	}

	// Connect to database
	ctx := context.Background()
	pool, err := newDBPool(ctx, dbURL)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		slog.Error("unable to ping database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to database")
	logPoolConfig(pool)

	bus := events.New()
	hub := realtime.NewHub()
	go hub.Run()
	daemonHub := daemonws.NewHub()
	var daemonWakeup service.TaskWakeupNotifier = daemonHub

	// MUL-1138: when REDIS_URL is set, route fanout through a Redis relay so
	// multiple API nodes can deliver each other's events. Without it the hub
	// is the sole broadcaster and the server stays single-node (legacy).
	// Runtime local-skill stores and realtime relay traffic use separate Redis
	// clients so blocking stream consumers cannot starve request-path Redis
	// operations.
	relayCtx, relayCancel := context.WithCancel(context.Background())
	var broadcaster realtime.Broadcaster = hub
	var storeRedis *redis.Client
	var relayWriteRedis *redis.Client
	var relayReadRedis *redis.Client
	var shardedReadRedis *redis.Client
	var legacyReadRedis *redis.Client
	var relay realtime.ManagedRelay
	defer func() {
		if relay != nil {
			relay.Stop()
		}
		relayCancel()
		if relay != nil {
			relay.Wait()
		}
		closeRedisClient("realtime-read-legacy", legacyReadRedis)
		closeRedisClient("realtime-read-sharded", shardedReadRedis)
		closeRedisClient("realtime-read", relayReadRedis)
		closeRedisClient("realtime-write", relayWriteRedis)
		closeRedisClient("store", storeRedis)
	}()
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			slog.Error("invalid REDIS_URL — falling back to in-memory hub", "error", err)
		} else {
			if envBool("REDIS_DISABLE_CLIENT_NAME", false) {
				slog.Info("redis: CLIENT SETNAME disabled (REDIS_DISABLE_CLIENT_NAME=true) for managed Redis compatibility")
			}
			storeRedis = newNamedRedisClient(opts, "store")
			relayWriteRedis = newNamedRedisClient(opts, "realtime-write")

			relayMode := realtimeRelayModeFromEnv()
			relayConfig := shardedRelayConfigFromEnv()
			switch relayMode {
			case "legacy":
				relayReadRedis = newNamedRedisClient(opts, "realtime-read")
				relay = realtime.NewRedisRelayWithClients(hub, relayWriteRedis, relayReadRedis)
				slog.Info("daemon websocket wakeup: Redis fanout disabled in legacy realtime relay mode")
			case "dual":
				shardedReadRedis = newNamedRedisClient(opts, "realtime-read-sharded")
				legacyReadRedis = newNamedRedisClient(opts, "realtime-read-legacy")
				sharded := realtime.NewShardedStreamRelay(hub, relayWriteRedis, shardedReadRedis, relayConfig)
				sharded.SetDaemonRuntimeDeliverer(daemonHub)
				legacy := realtime.NewRedisRelayWithClients(hub, relayWriteRedis, legacyReadRedis)
				relay = realtime.NewMirroredRelay(sharded, legacy)
				daemonWakeup = daemonws.NewRelayNotifier(daemonHub, sharded)
			default:
				relayReadRedis = newNamedRedisClient(opts, "realtime-read")
				sharded := realtime.NewShardedStreamRelay(hub, relayWriteRedis, relayReadRedis, relayConfig)
				sharded.SetDaemonRuntimeDeliverer(daemonHub)
				relay = sharded
				daemonWakeup = daemonws.NewRelayNotifier(daemonHub, sharded)
			}
			relay.Start(relayCtx)
			broadcaster = realtime.NewDualWriteBroadcaster(hub, relay)
			slog.Info(
				"realtime: Redis relay enabled",
				"node_id", relay.NodeID(),
				"mode", relayMode,
				"shards", relayConfig.Shards,
				"stream_max_len", relayConfig.StreamMaxLen,
				"xread_count", relayConfig.ReadCount,
				"xread_block", relayConfig.ReadBlock.String(),
				"store_pool_size", opts.PoolSize,
				"realtime_write_pool_size", opts.PoolSize,
				"realtime_read_pool_size", opts.PoolSize,
			)
		}
	} else {
		slog.Info("realtime: REDIS_URL not set — using in-memory hub (single-node mode)")
	}
	registerListeners(bus, broadcaster)

	analyticsClient := analytics.NewFromEnv()
	defer analyticsClient.Close()

	queries := db.New(pool)
	hub.SetAuthorizer(newScopeAuthorizer(queries))
	registerActivityListeners(bus, queries)

	metricsConfig := obsmetrics.ConfigFromEnv()
	var metricsServer *http.Server
	var httpMetrics *obsmetrics.HTTPMetrics
	var businessMetrics *obsmetrics.BusinessMetrics
	var samplerPool *pgxpool.Pool
	if metricsConfig.Enabled() {
		// Build a dedicated tiny pool for the BusinessSamplerCollector
		// so a stalled scrape can never starve business traffic. If the
		// pool fails to construct we log and continue without the
		// sampler — the rest of /metrics is still useful.
		var err error
		samplerPool, err = newSamplerDBPool(ctx, dbURL)
		if err != nil {
			slog.Warn("metrics: failed to build sampler pgxpool; sampler disabled", "error", err)
			samplerPool = nil
		}

		metricsRegistry := obsmetrics.NewRegistry(obsmetrics.RegistryOptions{
			Pool:     pool,
			Realtime: realtime.M,
			DaemonWS: daemonws.M,
			Version:  version,
			Commit:   commit,
			BusinessSampler: func() *obsmetrics.BusinessSamplerOptions {
				if samplerPool == nil {
					return nil
				}
				return &obsmetrics.BusinessSamplerOptions{Pool: samplerPool}
			}(),
		})
		httpMetrics = metricsRegistry.HTTP
		businessMetrics = metricsRegistry.Business
		// Forward inbound daemon WS frames into the per-kind counter so
		// dashboards can split heartbeat / unknown / invalid traffic.
		if daemonHub != nil {
			daemonHub.SetMessageKindRecorder(businessMetrics)
		}
		metricsServer = obsmetrics.NewServer(metricsConfig.Addr, metricsRegistry.Gatherer)
		if !obsmetrics.IsLoopbackAddr(metricsConfig.Addr) {
			slog.Warn(
				"metrics listener is not loopback-only; restrict access with private networking, allowlists, or proxy auth",
				"addr", metricsConfig.Addr,
			)
		}
	}
	if samplerPool != nil {
		defer samplerPool.Close()
	}

	// Construct the BatchedHeartbeatScheduler before the router so it can
	// be injected into the Handler. The Run goroutine starts below
	// alongside the sweeper, and Stop is called explicitly during graceful
	// shutdown so any pending bumps are flushed before we exit.
	heartbeatScheduler := handler.NewBatchedHeartbeatScheduler(queries, handler.DefaultHeartbeatBatchInterval)

	r, h := NewRouterWithOptions(pool, hub, bus, analyticsClient, storeRedis, RouterOptions{
		HTTPMetrics:        httpMetrics,
		BusinessMetrics:    businessMetrics,
		DaemonHub:          daemonHub,
		DaemonWakeup:       daemonWakeup,
		FeatureFlags:       flags,
		HeartbeatScheduler: heartbeatScheduler,
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Start background workers.
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	// Reuse the router-owned TaskService for the runtime sweeper. Mission/Run
	// reconciliation below owns orchestration lifecycle recovery.
	taskSvc := backgroundTaskService(h)
	runReconciler := h.Orchestration.NewRunReconciler(orchestration.RunReconcilerOptions{
		AfterReconcile: func(ctx context.Context, result orchestration.ReconcileRunResult) error {
			_, err := h.Orchestration.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{
				WorkspaceID:   result.Run.WorkspaceID,
				MissionID:     result.Run.MissionID,
				CorrelationID: result.Run.ID,
			})
			return err
		},
	})

	// Construct a LivenessStore that mirrors the one wired into the HTTP
	// handler. Both the heartbeat write path (handler) and the sweeper read
	// path (here) must agree on the same Redis-or-Noop choice; if they
	// disagree, online runtimes get falsely marked offline.
	var liveness handler.LivenessStore = handler.NewNoopLivenessStore()
	if storeRedis != nil {
		liveness = handler.NewRedisLivenessStore(storeRedis)
	}

	// Start background sweeper to mark stale runtimes as offline.
	go runRuntimeSweeper(sweepCtx, pool, queries, liveness, taskSvc, bus)
	go runReconciler.Run(sweepCtx)
	go heartbeatScheduler.Run(sweepCtx)
	go runDBStatsLogger(sweepCtx, pool)
	// GitHub PR-card API snapshot pipeline (MUL-5265): worker pool + TTL sweeper.
	// No-op when unconfigured (no App private key).
	h.PRRefresh.Start(sweepCtx)

	// MUL-2957: DB-backed execution scheduler. The scheduler turns the
	// `sys_cron_executions` table into the distributed lease + audit
	// log for internal periodic jobs. The first job is
	// `rollup_task_usage_hourly`, which replaces the previously
	// operator-registered `pg_cron` entry (still safe to run
	// concurrently — the SQL function holds advisory lock 4246).
	//
	// A failure to register the job is treated as fatal here only at
	// the registration step (a duplicate name is the only realistic
	// cause and indicates a code bug). Once running, the manager
	// surfaces transient errors — DB unreachable, sys_cron_executions
	// missing because of an unusual partial-migration state — by
	// logging them on the tick that fails and retrying on the next
	// cycle, so a temporary outage does not crash the server.
	schedulerMgr := scheduler.NewManager(pool, scheduler.Options{})
	if err := schedulerMgr.Register(scheduler.TaskUsageHourlyJob(pool)); err != nil {
		slog.Warn("scheduler: failed to register task_usage_hourly rollup job", "error", err)
	}
	go func() {
		_ = schedulerMgr.Run(sweepCtx)
	}()

	if metricsServer != nil {
		go func() {
			slog.Info("metrics server starting", "addr", metricsConfig.Addr)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server disabled after startup error", "error", err)
			}
		}()
	}

	go func() {
		slog.Info("server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	holdBeforeShutdown(sig, quit, shutdownHoldDuration)
	// Restore the default behavior so another signal during graceful shutdown
	// can still terminate the process instead of being left unread in quit.
	signal.Stop(quit)

	slog.Info("shutting down server")

	// Order matters: drain in-flight HTTP first so any heartbeat handlers
	// finish calling Schedule() before we stop the scheduler. Otherwise a
	// late heartbeat could enqueue a pending ID after Run has already
	// drained and exited, and Stop() would not flush it.
	apiShutdownCtx, apiShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := srv.Shutdown(apiShutdownCtx); err != nil {
		apiShutdownCancel()
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}
	apiShutdownCancel()

	// HTTP is fully drained — safe to stop the sweeper and flush the
	// final batch of queued heartbeat bumps.
	sweepCancel()
	heartbeatScheduler.Stop()

	if metricsServer != nil {
		metricsShutdownCtx, metricsShutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := metricsServer.Shutdown(metricsShutdownCtx); err != nil {
			slog.Error("metrics server forced to shutdown", "error", err)
		}
		metricsShutdownCancel()
	}
	slog.Info("server stopped")
}
