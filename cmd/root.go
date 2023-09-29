package cmd

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"time"

	"github.com/canonical/k8s-dqlite/pkg/server"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	rootCmdOpts struct {
		dir                    string
		listen                 string
		tls                    bool
		debug                  bool
		profiling              bool
		profilingAddress       string
		diskMode               bool
		clientSessionCacheSize uint
		minTLSVersion          string
		metrics                bool
		metricsAddress         string

		watchAvailableStorageInterval time.Duration
		watchAvailableStorageMinBytes uint64
		lowAvailableStorageAction     string
	}

	rootCmd = &cobra.Command{
		Use:   "k8s-dqlite",
		Short: "Dqlite for Kubernetes",
		Long:  `Kubernetes datastore based on dqlite`,
		// Uncomment the following line if your bare application
		// has an action associated with it:
		Run: func(cmd *cobra.Command, args []string) {
			if rootCmdOpts.debug {
				logrus.SetLevel(logrus.TraceLevel)
			}

			if rootCmdOpts.profiling {
				go func() {
					logrus.WithField("address", rootCmdOpts.profilingAddress).Print("Enable pprof endpoint")
					http.ListenAndServe(rootCmdOpts.profilingAddress, nil)
				}()
			}

			if rootCmdOpts.metrics {
				go func() {
					logrus.WithField("address", rootCmdOpts.metricsAddress).Print("Enable metrics endpoint")
					mux := http.NewServeMux()
					mux.Handle("/metrics", promhttp.Handler())
					http.ListenAndServe(rootCmdOpts.metricsAddress, mux)
				}()
			}

			server, err := server.New(
				rootCmdOpts.dir,
				rootCmdOpts.listen,
				rootCmdOpts.tls,
				rootCmdOpts.diskMode,
				rootCmdOpts.clientSessionCacheSize,
				rootCmdOpts.minTLSVersion,
				rootCmdOpts.watchAvailableStorageInterval,
				rootCmdOpts.watchAvailableStorageMinBytes,
				rootCmdOpts.lowAvailableStorageAction,
			)
			if err != nil {
				logrus.WithError(err).Fatal("Failed to create server")
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			if err := server.Start(ctx); err != nil {
				logrus.WithError(err).Fatal("Server failed to start")
			}

			// Cancel context if we receive an exit signal
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, unix.SIGPWR)
			signal.Notify(ch, unix.SIGINT)
			signal.Notify(ch, unix.SIGQUIT)
			signal.Notify(ch, unix.SIGTERM)

			select {
			case <-ch:
			case <-server.MustStop():
			}
			cancel()

			// Create a separate context with 30 seconds to cleanup
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := server.Shutdown(stopCtx); err != nil {
				logrus.WithError(err).Fatal("Failed to shutdown server")
			}
		},
	}
)

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the liteCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVar(&rootCmdOpts.dir, "storage-dir", "/var/tmp/k8s-dqlite", "directory with the dqlite datastore")
	rootCmd.Flags().StringVar(&rootCmdOpts.listen, "listen", "tcp://127.0.0.1:12379", "endpoint where dqlite should listen to")
	rootCmd.Flags().BoolVar(&rootCmdOpts.tls, "enable-tls", true, "enable TLS")
	rootCmd.Flags().BoolVar(&rootCmdOpts.debug, "debug", false, "debug logs")
	rootCmd.Flags().BoolVar(&rootCmdOpts.profiling, "profiling", false, "enable debug pprof endpoint")
	rootCmd.Flags().StringVar(&rootCmdOpts.profilingAddress, "profiling-listen", "127.0.0.1:4000", "listen address for pprof endpoint")
	rootCmd.Flags().BoolVar(&rootCmdOpts.diskMode, "disk-mode", false, "(experimental) run dqlite store in disk mode")
	rootCmd.Flags().UintVar(&rootCmdOpts.clientSessionCacheSize, "tls-client-session-cache-size", 0, "ClientCacheSession size for dial TLS config")
	rootCmd.Flags().StringVar(&rootCmdOpts.minTLSVersion, "min-tls-version", "tls12", "Minimum TLS version for dqlite endpoint (tls10|tls11|tls12|tls13). Default is tls12")
	rootCmd.Flags().BoolVar(&rootCmdOpts.metrics, "metrics", true, "enable metrics endpoint")
	rootCmd.Flags().StringVar(&rootCmdOpts.metricsAddress, "metrics-listen", "127.0.0.1:9042", "listen address for metrics endpoint")
	rootCmd.Flags().DurationVar(&rootCmdOpts.watchAvailableStorageInterval, "watch-storage-available-size-interval", 5*time.Second, "Interval to check if the disk is running low on space. Set to 0 to disable the periodic disk size check")
	rootCmd.Flags().Uint64Var(&rootCmdOpts.watchAvailableStorageMinBytes, "watch-storage-available-size-min-bytes", 10*1024*1024, "Minimum required available disk size (in bytes) to continue operation. If available disk space gets below this threshold, then the --low-available-storage-action is performed")
	rootCmd.Flags().StringVar(&rootCmdOpts.lowAvailableStorageAction, "low-available-storage-action", "none", "Action to perform in case the available storage is low. One of (none|handover|terminate). none means no action is performed. handover means the dqlite node will handover its leadership role, if any. terminate means this dqlite node will shutdown")
}