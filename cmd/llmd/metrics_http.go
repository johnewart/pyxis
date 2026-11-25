package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/johnewart/pyxis/llmdhost"
	"github.com/johnewart/pyxis/llmdlog"
)

// envMetricsAddr overrides where the Prometheus /metrics listener binds.
const envMetricsAddr = "LLMD_METRICS_ADDR"

// metricsAutoAddr is the dev-mode default: an ephemeral loopback port, like the
// debug listener's, so several llmd copies on one box never collide.
const metricsAutoAddr = "127.0.0.1:0"

// resolveMetricsAddr decides where /metrics binds. An explicit
// LLMD_METRICS_ADDR always wins and may bind any interface (metrics carry no
// pprof, so the debug listener's loopback rule does not apply). Otherwise
// dev mode (LLMD_DEBUG=1) turns it on at an ephemeral loopback port, and
// without either it is off ("").
func resolveMetricsAddr(explicit string, debug bool) (addr, trigger string) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, envMetricsAddr
	}
	if debug {
		return metricsAutoAddr, llmdlog.EnvDebug + "=1"
	}
	return "", ""
}

// metricsMux mounts only GET /metrics; nothing else is served on this
// listener, so it is safe to expose off-box.
func metricsMux(metrics http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	return mux
}

// startMetricsHTTP binds the Prometheus listener and advertises its state at
// startup either way: the URL when on, how to turn it on when off.
func startMetricsHTTP(host *llmdhost.Host) net.Listener {
	addr, trigger := resolveMetricsAddr(os.Getenv(envMetricsAddr), llmdhost.DebugEnabled())
	if addr == "" {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
			"llmd: metrics HTTP disabled (set %s=1 for an auto ephemeral port, or %s=HOST:PORT; serves Prometheus /metrics)",
			llmdlog.EnvDebug, envMetricsAddr)
		return nil
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "%s listen %s: %v", envMetricsAddr, addr, err)
	}
	url := fmt.Sprintf("http://%s/metrics", lis.Addr().String())
	scope := "loopback only"
	if !isLoopbackAddr(addr) {
		scope = "NOT loopback: reachable off-box"
	}
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "========================================================================")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "  llmd METRICS HTTP (%s)  pid=%d", trigger, os.Getpid())
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "    prometheus : %s", url)
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "    (%s)", scope)
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "========================================================================")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "")
	handler := metricsMux(host.MetricsHandler())
	go func() {
		if err := http.Serve(lis, handler); err != nil {
			llmdlog.Stdout(logModuleMain, llmdlog.LevelWarn, "llmd: metrics HTTP serve ended on %s: %v", lis.Addr(), err)
		}
	}()
	return lis
}
