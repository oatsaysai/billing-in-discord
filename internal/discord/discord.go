package discord

import (
	"fmt"
	"log"
	"net/http"
	"regexp"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/discord/handlers"
	"github.com/oatsaysai/billing-in-discord/pkg/firebase"
	"github.com/oatsaysai/billing-in-discord/pkg/ocr"
	"github.com/oatsaysai/billing-in-discord/pkg/verifier"
)

var (
	session          *discordgo.Session
	userMentionRegex = regexp.MustCompile(`<@!?(\d+)>`)
	txIDRegex        = regexp.MustCompile(`\(TxID:\s?(\d+)\)`)
	txIDsRegex       = regexp.MustCompile(`\(TxIDs:\s?([\d,]+)\)`)
	verifierClient   *verifier.Client
	ocrClient        *ocr.Client
	firebaseClient   *firebase.Client
)

// SetFirebaseClient sets the Firebase client
func SetFirebaseClient(client *firebase.Client) {
	firebaseClient = client
	handlers.SetFirebaseClient(client)
}

// SetOCRClient sets the OCR client
func SetOCRClient(client *ocr.Client) {
	ocrClient = client
	handlers.SetOCRClient(client)
}

// SetVerifierClient sets the verifier client
func SetVerifierClient(client *verifier.Client) {
	verifierClient = client
	handlers.SetVerifierClient(client)
}

// Initialize sets up the Discord session and registers handlers
func Initialize(token string) error {
	var err error
	session, err = discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("error creating Discord session: %w", err)
	}

	// Set session in handlers package
	handlers.SetDiscordSession(session)

	// Update the registry with all commands
	UpdateRegistry()

	// Register the message handler
	session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		ProcessCommand(s, m)
	})

	// Register component handlers for interactive UI
	handlers.RegisterComponentHandlers(session)

	// Open connection to Discord
	err = session.Open()
	if err != nil {
		return fmt.Errorf("error opening connection to Discord: %w", err)
	}

	log.Println("Connected to Discord successfully")
	return nil
}

// Close closes the Discord session
func Close() {
	if session != nil {
		session.Close()
	}
}

// GetSession returns the Discord session
func GetSession() *discordgo.Session {
	return session
}

// HandleBillWebhookCallback is a bridge to the handler's implementation
func HandleBillWebhookCallback(w http.ResponseWriter, r *http.Request) {
	handlers.HandleBillWebhookCallback(w, r)
}
