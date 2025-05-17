package handlers

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Component Custom IDs - shared constants for all interactive components
const (
	payDebtButtonPrefix        = "pay_debt_"
	viewDetailButtonPrefix     = "view_detail_"
	requestPaymentButtonPrefix = "request_payment_"
	markPaidButtonPrefix       = "mark_paid_"
	confirmPaymentButtonPrefix = "confirm_payment_"
	confirmPaymentNoSlipPrefix = "confirm_payment_no_slip_"
	verifyPaymentConfirmPrefix = "verify_payment_confirm_"
	verifyPaymentRejectPrefix  = "verify_payment_reject_"
	billAllocateButtonPrefix   = "bill_allocate_"
	billSkipButtonPrefix       = "bill_skip_"
	billCancelButtonID         = "bill_cancel"
	billUsersSelectPrefix      = "bill_users_select_"
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
	case strings.HasPrefix(customID, requestPaymentButtonPrefix):
		handleRequestPaymentButton(s, i)
	case strings.HasPrefix(customID, markPaidButtonPrefix):
		handleMarkPaidButton(s, i)
	case strings.HasPrefix(customID, confirmPaymentButtonPrefix):
		handleConfirmPaymentButton(s, i)
	case strings.HasPrefix(customID, confirmPaymentNoSlipPrefix):
		handleConfirmPaymentNoSlipButton(s, i)
	case strings.HasPrefix(customID, verifyPaymentConfirmPrefix):
		handleVerifyPaymentConfirmButton(s, i)
	case strings.HasPrefix(customID, verifyPaymentRejectPrefix):
		handleVerifyPaymentRejectButton(s, i)
	case customID == debtDropdownID:
		handleDebtDropdown(s, i)
	case strings.HasPrefix(customID, billAllocateButtonPrefix):
		handleBillAllocateButton(s, i)
	case strings.HasPrefix(customID, billSkipButtonPrefix):
		handleBillSkipButton(s, i)
	case customID == billCancelButtonID:
		handleBillCancelButton(s, i)
	case strings.HasPrefix(customID, billUsersSelectPrefix):
		handleUserSelectSubmit(s, i)
	default:
		log.Printf("Unknown component interaction: %s", customID)
		respondWithError(s, i, "ไม่รู้จัก interaction นี้ โปรดติดต่อผู้ดูแลระบบ")
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

	if strings.HasPrefix(customID, "modal_pay_debt_") {
		handlePayDebtModalSubmit(s, i)
	} else {
		log.Printf("Unknown modal interaction: %s", customID)
		respondWithError(s, i, "ไม่รู้จัก modal interaction นี้ โปรดติดต่อผู้ดูแลระบบ")
	}
}
