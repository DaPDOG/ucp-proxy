// UCP Proxy - Translates UCP protocol to WooCommerce Store API.
// Designed for Cloud Run deployment with stateless operation.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/config"
	"ucp-proxy/internal/handler"
	"ucp-proxy/internal/middleware"
	"ucp-proxy/internal/negotiation"
	"ucp-proxy/internal/wix"
	"ucp-proxy/internal/woocommerce"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize structured logger
	logger := initLogger()

	// Load configuration
	ctx := context.Background()
	cfg, err := config.Load(ctx)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Info("configuration loaded",
		slog.String("merchant_id", cfg.MerchantID),
		slog.String("adapter_type", cfg.AdapterType),
		slog.String("environment", cfg.Environment),
		slog.String("store_domain", cfg.Merchant.StoreDomain),
	)

	// Create adapter based on configuration
	adapterInstance, err := createAdapter(cfg)
	if err != nil {
		return fmt.Errorf("creating adapter: %w", err)
	}

	// Get business profile for negotiation
	businessProfile, err := adapterInstance.GetProfile(ctx)
	if err != nil {
		return fmt.Errorf("getting business profile: %w", err)
	}

	// Create negotiator for UCP capability negotiation (spec Section 5)
	fetcher := negotiation.NewHTTPProfileFetcher()
	negotiator := negotiation.NewNegotiator(fetcher, businessProfile)

	// Create handler with adapter and negotiator
	h := handler.New(adapterInstance, negotiator, logger)

	// Setup routes
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Apply middleware chain: recovery → logging → negotiation → handler
	// Recovery must be outermost to catch panics from logging middleware
	// Negotiation enforces UCP-Agent header on all requests (except exempt paths)
	httpHandler := middleware.Chain(
		middleware.Recovery(logger),
		middleware.Logging(logger),
		negotiation.Middleware(negotiator, logger),
	)(mux)

	// Create HTTP server with timeouts
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      httpHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Channel for shutdown signals
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Channel for server errors
	serverErr := make(chan error, 1)

	// Start server in goroutine
	go func() {
		logger.Info("server starting",
			slog.String("port", cfg.Port),
			slog.String("addr", server.Addr),
		)
		serverErr <- server.ListenAndServe()
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErr:
		if err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}

	case sig := <-shutdown:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))

		// Give outstanding requests time to complete
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			// Force close if graceful shutdown fails
			server.Close()
			return fmt.Errorf("shutdown error: %w", err)
		}
	}

	logger.Info("server stopped")
	return nil
}

// createAdapter creates the appropriate adapter based on configuration.
func createAdapter(cfg *config.Config) (adapter.Adapter, error) {
	switch cfg.AdapterType {
	case "woocommerce":
		return woocommerce.New(woocommerce.Config{
			StoreURL:        cfg.Merchant.StoreURL,
			APIKey:          cfg.Merchant.APIKey,
			APISecret:       cfg.Merchant.APISecret,
			TransformConfig: cfg.BuildTransformConfig(),
		})
	case "wix":
		return wix.New(wix.Config{
			ClientID:        cfg.Merchant.WixClientID,
			TransformConfig: cfg.BuildTransformConfig(),
		})
	default:
		return nil, fmt.Errorf("unsupported adapter type: %s", cfg.AdapterType)
	}
}

// initLogger creates a structured logger configured for the environment.
// Production uses JSON format for GCP Cloud Logging compatibility.
// Development uses text format for readability.
func initLogger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: level,
		// Add source location in debug mode
		AddSource: level == slog.LevelDebug,
	}

	// JSON for production (Cloud Logging compatible), text for development
	if os.Getenv("ENVIRONMENT") == "production" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
