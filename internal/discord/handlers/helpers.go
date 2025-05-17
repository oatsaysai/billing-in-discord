package handlers

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	pp "github.com/Frontware/promptpay"
	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
)

var (
	userMentionRegex = regexp.MustCompile(`<@!?(\d+)>`)
	txIDRegex        = regexp.MustCompile(`\(TxID:\s?(\d+)\)`)
	txIDsRegex       = regexp.MustCompile(`\(TxIDs:\s?([\d,]+)\)`)
)

// sendErrorMessage sends an error message to the specified Discord channel
func SendErrorMessage(s *discordgo.Session, channelID, message string) {
	log.Printf("ERROR to user (Channel: %s): %s", channelID, message)
	_, err := s.ChannelMessageSend(channelID, fmt.Sprintf("⚠️ เกิดข้อผิดพลาด: %s", message))
	if err != nil {
		log.Printf("Failed to send error message to Discord: %v", err)
	}
}

// generateAndSendQrCode generates a QR code and sends it to the specified Discord channel
func GenerateAndSendQrCode(s *discordgo.Session, channelID, promptPayNum string, amount float64, targetUserDiscordID, description string, txIDs []int) {
	payment := pp.PromptPay{PromptPayID: promptPayNum, Amount: amount}
	qrcodeStr, err := payment.Gen()
	if err != nil {
		SendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถสร้างข้อมูล QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Error generating PromptPay string for %s: %v", targetUserDiscordID, err)
		return
	}
	qrc, err := qrcode.New(qrcodeStr)
	if err != nil {
		SendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถสร้างรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Error generating QR code for %s: %v", targetUserDiscordID, err)
		return
	}
	filename := fmt.Sprintf("qr_%s_%d.jpg", targetUserDiscordID, time.Now().UnixNano())
	fileWriter, err := standard.New(filename)
	if err != nil {
		SendErrorMessage(s, channelID, fmt.Sprintf("การสร้างรูปภาพ QR สำหรับ <@%s> ล้มเหลวภายในระบบ", targetUserDiscordID))
		log.Printf("standard.New failed for QR %s: %v", targetUserDiscordID, err)
		return
	}
	if err = qrc.Save(fileWriter); err != nil {
		SendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถบันทึกรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Could not save QR image for %s: %v", targetUserDiscordID, err)
		os.Remove(filename) // Clean up
		return
	}
	file, err := os.Open(filename)
	if err != nil {
		SendErrorMessage(s, channelID, fmt.Sprintf("ไม่สามารถส่งรูปภาพ QR สำหรับ <@%s> ได้", targetUserDiscordID))
		log.Printf("Could not open QR image %s for sending: %v", filename, err)
		os.Remove(filename) // Clean up
		return
	}
	defer file.Close()
	defer os.Remove(filename) // Clean up

	txIDString := ""
	if len(txIDs) == 1 {
		txIDString = fmt.Sprintf(" (TxID: %d)", txIDs[0])
	} else if len(txIDs) > 1 {
		var idStrs []string
		for _, id := range txIDs {
			idStrs = append(idStrs, fmt.Sprintf("%d", id))
		}
		txIDString = fmt.Sprintf(" (TxIDs: %s)", strings.Join(idStrs, ","))
	}

	msgContent := fmt.Sprintf("<@%s> กรุณาชำระ %.2f บาท สำหรับ \"%s\"%s\nหากต้องการยืนยันการชำระเงิน ตอบกลับข้อความนี้พร้อมแนบสลิปของคุณ", targetUserDiscordID, amount, description, txIDString)
	if description == "" {
		msgContent = fmt.Sprintf("<@%s> กรุณาชำระ %.2f บาท%s\nหากต้องการยืนยันการชำระเงิน ตอบกลับข้อความนี้พร้อมแนบสลิปของคุณ", targetUserDiscordID, amount, txIDString)
	}

	_, err = s.ChannelFileSendWithMessage(channelID, msgContent, filename, file)
	if err != nil {
		log.Printf("Failed to send QR file for %s: %v", targetUserDiscordID, err)
	}
}

// GetDiscordUsername retrieves a user's display name from their Discord ID
func GetDiscordUsername(s *discordgo.Session, discordID string) string {
	// Check if session is nil
	if s == nil {
		log.Println("ERROR: Discord session is nil in GetDiscordUsername")
		return "User"
	}

	// Fetch user info from Discord API
	user, err := s.User(discordID)
	if err != nil {
		log.Printf("Error fetching user info for ID %s: %v", discordID, err)
		return "User"
	}

	// Use global_name if available, otherwise username
	if user.GlobalName != "" {
		return user.GlobalName
	}
	if user.Username != "" {
		return user.Username
	}

	return "User"
}

// EnhanceDebtsWithUsernames adds Discord usernames to debt details
func EnhanceDebtsWithUsernames(s *discordgo.Session, debts []db.DebtDetail) {
	for i := range debts {
		// Remove @ or <> characters from Discord ID if present
		discordID := strings.TrimPrefix(debts[i].OtherPartyDiscordID, "@")
		discordID = strings.TrimPrefix(discordID, "<@")
		discordID = strings.TrimSuffix(discordID, ">")

		// Fetch username from Discord and set it in OtherPartyName
		debts[i].OtherPartyName = GetDiscordUsername(s, discordID)
	}
}
