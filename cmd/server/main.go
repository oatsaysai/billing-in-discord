package main

import (
	"context"
	"flag"
	"github.com/oatsaysai/billing-in-discord/pkg/verifier"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oatsaysai/billing-in-discord/internal/config"
	"github.com/oatsaysai/billing-in-discord/internal/db"
	"github.com/oatsaysai/billing-in-discord/internal/discord"
	"github.com/oatsaysai/billing-in-discord/pkg/firebase"
	"github.com/oatsaysai/billing-in-discord/pkg/ocr"
	"github.com/spf13/viper"
)

func main() {
	// Parse command-line flags
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Initialize configuration
	config.Initialize()
	log.Printf("Using config file: %s", *configFile)

	// Initialize database
	db.Initialize()
	db.Migrate()

	// Initialize Firebase client
	fbClient := firebase.NewClient(
		viper.GetString("Firebase.CliPath"),
		viper.GetString("Firebase.MainProjectID"),
		viper.GetString("Firebase.SiteNamePrefix"),
	)
	log.Printf("Firebase client initialized for project: %s", viper.GetString("Firebase.MainProjectID"))

	// Set the Firebase client in the discord package
	discord.SetFirebaseClient(fbClient)

	// Initialize Slip Verifier client
	verifierClient := verifier.NewClient(
		viper.GetString("SlipVerifier.ApiUrl"),
	)
	log.Printf("Slip Verifier client initialized with API URL: %s", viper.GetString("SlipVerifier.ApiUrl"))

	// Set the verifier client in the discord package
	discord.SetVerifierClient(verifierClient)

	// Initialize OCR client
	ocrClient := ocr.NewClient(
		viper.GetString("OCR.ApiUrl"),
		viper.GetString("OCR.ApiKey"),
	)
	log.Printf("OCR client initialized with API URL: %s", viper.GetString("OCR.ApiUrl"))

	// Set the OCR client in the discord package
	discord.SetOCRClient(ocrClient)

	// Start HTTP server for webhook callbacks
	go setupHTTPServer()

	// Initialize Discord bot
	if err := discord.Initialize(viper.GetString("DiscordBot.Token")); err != nil {
		log.Fatalf("Failed to initialize Discord bot: %v", err)
	}
	defer discord.Close()

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle termination signals
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for termination signal
	go func() {
		<-signalChan
		log.Println("Received termination signal, shutting down gracefully...")
		cancel()
	}()

	log.Println("Billing in Discord bot is now running. Press CTRL+C to exit.")

	// Setup periodic cleanup of expired Firebase sites
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			discord.CleanupExpiredSites()
		}
	}()
	defer ticker.Stop()

	// Keep the application running until context is cancelled
	<-ctx.Done()
	log.Println("Billing in Discord bot shutting down...")
}

// setupHTTPServer initializes and starts the HTTP server for webhook callbacks
func setupHTTPServer() {
	mux := http.NewServeMux()

	// Register route for webhook callback
	mux.HandleFunc("/api/bill-webhook", discord.HandleBillWebhookCallback)

	// Add middleware for CORS
	handler := corsMiddleware(mux)

	// Get port from configuration
	port := viper.GetString("Server.Port")
	if port == "" {
		port = "8080"
	}

	serverAddr := ":" + port
	log.Printf("Starting HTTP server on %s", serverAddr)

	if err := http.ListenAndServe(serverAddr, handler); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}

// corsMiddleware handles CORS headers for cross-domain requests
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*") // Or specify allowed domains
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Pass to next handler
		next.ServeHTTP(w, r)
	})
}
