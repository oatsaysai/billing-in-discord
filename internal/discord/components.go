package discord

import (
	"github.com/bwmarrin/discordgo"
	"log"
	"strings"
)

const (
	// Component Custom IDs
	payDebtButtonPrefix        = "pay_debt_"
	viewDetailButtonPrefix     = "view_detail_"
	requestPaymentButtonPrefix = "request_payment_"
	markPaidButtonPrefix       = "mark_paid_"
	confirmPaymentButtonPrefix = "confirm_payment_"
	viewDuesButtonPrefix       = "view_dues_"
	debtDropdownID             = "debt_dropdown"
)

// RegisterComponentHandlers registers the interaction handlers for components
func RegisterComponentHandlers(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type == discordgo.InteractionMessageComponent {
			handleMessageComponentInteraction(s, i)
		} else if i.Type == discordgo.InteractionModalSubmit {
			handleModalSubmit(s, i)
		}
	})
}

// handleMessageComponentInteraction routes component interactions to the appropriate handler
func handleMessageComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	switch {
	case strings.HasPrefix(customID, payDebtButtonPrefix):
		handlePayDebtButton(s, i)
	case strings.HasPrefix(customID, viewDetailButtonPrefix):
		handleViewDetailButton(s, i)
	case strings.HasPrefix(customID, markPaidButtonPrefix):
		handleMarkPaidButton(s, i)
	case strings.HasPrefix(customID, confirmPaymentButtonPrefix):
		handleConfirmPaymentButton(s, i)
	case strings.HasPrefix(customID, requestPaymentButtonPrefix):
		handleRequestPaymentButton(s, i)
	case strings.HasPrefix(customID, viewDuesButtonPrefix):
		handleViewDuesButton(s, i)
	case customID == debtDropdownID:
		handleDebtDropdown(s, i)
	default:
		log.Printf("Unknown component interaction: %s", customID)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "ไม่รู้จัก interaction นี้ โปรดติดต่อผู้ดูแลระบบ",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}
}

// respondWithError sends an ephemeral error message
func respondWithError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "⚠️ " + message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleModalSubmit handles modal submission interactions
func handleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.ModalSubmitData().CustomID

	// Example: modal_pay_debt_<txID>
	if strings.HasPrefix(customID, "modal_pay_debt_") {
		handlePayDebtModalSubmit(s, i)
	} else {
		log.Printf("Unknown modal interaction: %s", customID)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "ไม่รู้จัก modal interaction นี้ โปรดติดต่อผู้ดูแลระบบ",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}
}
