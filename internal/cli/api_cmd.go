package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/markx3/agentboard/internal/api"
	"github.com/markx3/agentboard/internal/config"
)

var (
	apiHost string
	apiPort int
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Expose the agentboard CLI as an HTTP API",
	RunE:  runAPIServer,
}

func init() {
	apiCmd.Flags().StringVar(&apiHost, "bind", "127.0.0.1", "address to bind to")
	apiCmd.Flags().IntVar(&apiPort, "port", 8080, "port to listen on (set PORT env var to override)")
	rootCmd.AddCommand(apiCmd)
}

func runAPIServer(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	apiKey, err := config.EnsureAPIKey()
	if err != nil {
		return fmt.Errorf("ensuring API key: %w", err)
	}

	svc, cleanup, err := openService()
	if err != nil {
		return err
	}
	defer cleanup()

	port := apiPort
	if !cmd.Flags().Changed("port") {
		if envPort := os.Getenv("PORT"); envPort != "" {
			val, convErr := strconv.Atoi(envPort)
			if convErr != nil {
				return fmt.Errorf("invalid PORT value %q: %w", envPort, convErr)
			}
			port = val
		}
	}

	if port < 0 || port > 65535 {
		return fmt.Errorf("invalid port: %d", port)
	}

	addr := fmt.Sprintf("%s:%d", apiHost, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	defer ln.Close()

	server := &http.Server{
		Handler: api.NewServer(svc, apiKey),
	}

	fmt.Fprintf(os.Stderr, "agentboard API listening on %s\n", ln.Addr().String())
	fmt.Fprintf(os.Stderr, "Use header X-API-Key with the value in AGENTBOARD_API_KEY (.env)\n")

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)

	err = <-errCh
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
