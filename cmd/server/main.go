package main

import (
	"context"
	"flag"
	"github.com/oatsaysai/billing-in-discord/pkg/verifier"
	"log"
	"os"
	"os/signal"
	"syscall"

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
	_ = fbClient // Using the client to avoid unused variable warning

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
	// Keep the application running until context is cancelled
	<-ctx.Done()
	log.Println("Billing in Discord bot shutting down...")
}
