package discord

import (
	"fmt"
	"github.com/oatsaysai/billing-in-discord/internal/db"
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

// CleanupExpiredSites deletes expired Firebase sites (older than specified minutes)
func CleanupExpiredSites() {
	if firebaseClient == nil {
		log.Println("Firebase client not initialized, skipping site cleanup")
		return
	}

	// Get expired sites from database (older than 30 minutes)
	expiredSites, err := db.GetExpiredFirebaseSites(30) // 30 minutes
	if err != nil {
		log.Printf("Error getting expired Firebase sites: %v", err)
		return
	}

	if len(expiredSites) == 0 {
		return // No expired sites to cleanup
	}

	log.Printf("Found %d expired Firebase sites to cleanup", len(expiredSites))

	for _, site := range expiredSites {
		log.Printf("Cleaning up expired Firebase site: %s (created %v)", site.SiteName, site.CreatedAt)
		if err := firebaseClient.DeleteSite(site.SiteName); err != nil {
			log.Printf("Error deleting expired Firebase site %s: %v", site.SiteName, err)
		} else {
			// Update site status in the database
			db.UpdateFirebaseSiteStatus(site.SiteName, "inactive")
			log.Printf("Successfully deleted expired Firebase site %s", site.SiteName)

			// Try to find owner using site_token and clean up memory storage in handlers
			if site.SiteToken != "" {
				handlers.CleanupSessionDataByToken(site.SiteToken)
				log.Printf("Cleaned up memory storage for token %s", site.SiteToken)
			}
		}
	}
}

// HandleBillWebhookCallback is a bridge to the handler's implementation
func HandleBillWebhookCallback(w http.ResponseWriter, r *http.Request) {
	handlers.HandleBillWebhookCallback(w, r)
}
