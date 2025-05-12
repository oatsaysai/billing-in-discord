package discord

import (
	"fmt"
	"github.com/oatsaysai/billing-in-discord/pkg/ocr"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
)

// handleOCRBillAttachment processes the bill image using OCR
func handleOCRBillAttachment(s *discordgo.Session, m *discordgo.MessageCreate, attachment *discordgo.MessageAttachment) {
	// Check if OCR client is configured
	if ocrClient == nil {
		sendErrorMessage(s, m.ChannelID, "OCR service is not configured. OCR bill processing is not available.")
		return
	}

	// Check if it's an image file
	if !strings.HasPrefix(strings.ToLower(attachment.ContentType), "image/") {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("ไฟล์ที่แนบมาไม่ใช่รูปภาพ (%s)", attachment.ContentType))
		return
	}

	// Download the image
	tmpFile := fmt.Sprintf("bill_%s_%d.png", m.ID, time.Now().UnixNano())
	err := downloadFile(tmpFile, attachment.URL)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถดาวน์โหลดไฟล์รูปภาพบิลได้")
		log.Printf("OCRBill: Failed to download bill image %s: %v", attachment.URL, err)
		return
	}
	defer os.Remove(tmpFile) // Clean up temporary file when done

	// Send a message to indicate processing
	processingMsg, err := s.ChannelMessageSend(m.ChannelID, "⏳ กำลังประมวลผลรูปภาพบิลผ่าน OCR...")
	if err != nil {
		log.Printf("OCRBill: Failed to send processing message: %v", err)
		// Continue processing even if we couldn't send the message
	}

	// Process the image with OCR
	billData, err := ocrClient.ExtractBillText(tmpFile)
	if err != nil {
		sendErrorMessage(s, m.ChannelID, fmt.Sprintf("การประมวลผล OCR ล้มเหลว: %v", err))
		log.Printf("OCRBill: OCR processing failed for %s: %v", attachment.URL, err)
		// Clean up the processing message
		if processingMsg != nil {
			s.ChannelMessageDelete(m.ChannelID, processingMsg.ID)
		}
		return
	}

	// Update the processing message to indicate success
	if processingMsg != nil {
		s.ChannelMessageEdit(m.ChannelID, processingMsg.ID, "✅ ประมวลผลรูปภาพบิลสำเร็จ! กำลังแสดงผลลัพธ์...")
	}

	// Generate a summary of the bill
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("**ผลการวิเคราะห์บิลด้วย OCR**\n"))
	summary.WriteString(fmt.Sprintf("📇 **ร้าน**: %s\n", billData.MerchantName))
	summary.WriteString(fmt.Sprintf("📅 **วันที่เวลา**: %s\n", billData.Datetime))
	summary.WriteString(fmt.Sprintf("💰 **ยอดรวม**: %.2f บาท\n\n", billData.Total))

	summary.WriteString("**รายการ**:\n")
	for i, item := range billData.Items {
		total := float64(item.Quantity) * item.Price
		summary.WriteString(fmt.Sprintf("%d. %s x%d = %.2f บาท\n", i+1, item.Name, item.Quantity, total))
	}

	if billData.SubTotal > 0 {
		summary.WriteString(fmt.Sprintf("\nSubtotal: %.2f บาท\n", billData.SubTotal))
	}
	if billData.VAT > 0 {
		summary.WriteString(fmt.Sprintf("VAT: %.2f บาท\n", billData.VAT))
	}
	if billData.ServiceCharge > 0 {
		summary.WriteString(fmt.Sprintf("Service Charge: %.2f บาท\n", billData.ServiceCharge))
	}

	summary.WriteString("\nคลิกปุ่มด้านล่างเพื่อระบุรายการที่แต่ละคนต้องจ่าย")

	// Create button for opening the bill allocation modal
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "ระบุรายการของแต่ละคน",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("bill_allocate_%s", m.ID),
				},
			},
		},
	}

	// Send the summary with button
	_, err = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:    summary.String(),
		Components: components,
	})
	if err != nil {
		log.Printf("OCRBill: Failed to send bill OCR results: %v", err)
		sendErrorMessage(s, m.ChannelID, "ไม่สามารถส่งผลการวิเคราะห์บิลได้")
	}

	// Clean up the processing message
	if processingMsg != nil {
		s.ChannelMessageDelete(m.ChannelID, processingMsg.ID)
	}

	// Store the bill data in memory for later use when allocating
	storeBillOCRData(m.ID, billData)
}

// Global map to store OCR bill data temporarily
var billOCRDataStore = make(map[string]*ocr.ExtractBillTextResponse)

// storeBillOCRData stores the bill data in memory
func storeBillOCRData(messageID string, data *ocr.ExtractBillTextResponse) {
	billOCRDataStore[messageID] = data
}

// getBillOCRData retrieves the bill data from memory
func getBillOCRData(messageID string) *ocr.ExtractBillTextResponse {
	data, exists := billOCRDataStore[messageID]
	if !exists {
		return nil
	}
	return data
}

// handleBillAllocateButton handles the button click to allocate bill items
func handleBillAllocateButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) < 3 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	// Extract the message ID that contains the bill data
	messageID := parts[2]

	// Get the bill data from memory
	billData := getBillOCRData(messageID)
	if billData == nil {
		respondWithError(s, i, "ไม่พบข้อมูลบิล หรือข้อมูลหมดอายุแล้ว")
		return
	}

	// Create text input components for each item in the bill
	var components []discordgo.MessageComponent

	// Create text inputs with a reasonable limit
	itemLimit := 5 // Limit to 5 items due to Discord's modal component limit
	for idx, item := range billData.Items {
		if idx >= itemLimit {
			break
		}

		itemTotal := item.Price * float64(item.Quantity)
		itemLabel := fmt.Sprintf("%s (%.2f บาท)", item.Name, itemTotal)

		// Create a text input for each item
		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID:    fmt.Sprintf("item_%d", idx),
					Label:       itemLabel,
					Style:       discordgo.TextInputShort,
					Placeholder: "ระบุ @user1 @user2 หรือ all สำหรับทุกคน",
					Required:    false,
					MinLength:   0,
					MaxLength:   100,
				},
			},
		})
	}

	// Add an extra text input for additional items or special charges
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.TextInput{
				CustomID:    "additional_items",
				Label:       "รายการเพิ่มเติมหรือค่าบริการอื่นๆ",
				Style:       discordgo.TextInputParagraph,
				Placeholder: "ระบุรายการเพิ่มเติม เช่น 'ค่าบริการ 100 @user1 @user2' หรือเว้นว่างไว้",
				Required:    false,
				MinLength:   0,
				MaxLength:   300,
			},
		},
	})

	// Add a text input for promptpay ID
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.TextInput{
				CustomID:    "promptpay_id",
				Label:       "PromptPay ID (ถ้ามี)",
				Style:       discordgo.TextInputShort,
				Placeholder: "PromptPay ID ของผู้รับเงิน หรือเว้นว่างเพื่อใช้ค่าที่บันทึกไว้",
				Required:    false,
				MinLength:   0,
				MaxLength:   20,
			},
		},
	})

	// Create and show the modal
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID:   fmt.Sprintf("modal_bill_allocate_%s", messageID),
			Title:      "ระบุผู้ร่วมจ่ายในแต่ละรายการ",
			Components: components,
		},
	})

	if err != nil {
		log.Printf("Error showing bill allocation modal: %v", err)
	}
}

// handleBillAllocateModalSubmit handles the submission of the bill allocation modal
func handleBillAllocateModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	// Get the message ID from the modal custom ID
	// Format: modal_bill_allocate_<messageID>
	parts := strings.Split(data.CustomID, "_")
	if len(parts) < 4 {
		respondWithError(s, i, "รูปแบบ modal ID ไม่ถูกต้อง")
		return
	}

	messageID := parts[3]

	// Get the bill data from memory
	billData := getBillOCRData(messageID)
	if billData == nil {
		respondWithError(s, i, "ไม่พบข้อมูลบิล หรือข้อมูลหมดอายุแล้ว")
		return
	}

	// Extract the inputs from the modal
	itemAllocations := make(map[int][]string) // Map of item index to list of user IDs
	additionalItems := ""
	promptPayID := ""

	for _, comp := range data.Components {
		row, ok := comp.(discordgo.ActionsRow)
		if !ok {
			continue
		}

		for _, c := range row.Components {
			textInput, ok := c.(discordgo.TextInput)
			if !ok {
				continue
			}

			if strings.HasPrefix(textInput.CustomID, "item_") {
				// Extract item index
				idxStr := strings.TrimPrefix(textInput.CustomID, "item_")
				idx, err := strconv.Atoi(idxStr)
				if err != nil {
					continue
				}

				// Parse user mentions or "all"
				if textInput.Value == "all" {
					itemAllocations[idx] = []string{"all"}
				} else {
					// Extract user mentions
					mentions := userMentionRegex.FindAllStringSubmatch(textInput.Value, -1)
					if len(mentions) > 0 {
						var userIDs []string
						for _, mention := range mentions {
							userIDs = append(userIDs, mention[1])
						}
						itemAllocations[idx] = userIDs
					}
				}
			} else if textInput.CustomID == "additional_items" {
				additionalItems = textInput.Value
			} else if textInput.CustomID == "promptpay_id" {
				promptPayID = textInput.Value
			}
		}
	}

	// Acknowledge the modal submission
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "กำลังดำเนินการสร้างบิล...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Process the bill and create transactions
	successMsg, err := processBillAllocation(s, i, billData, itemAllocations, additionalItems, promptPayID)
	if err != nil {
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: fmt.Sprintf("⚠️ เกิดข้อผิดพลาดในการสร้างบิล: %v", err),
		})
		return
	}

	// Send a success message
	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: successMsg,
	})

	// Clean up the bill data from memory
	delete(billOCRDataStore, messageID)
}

// processBillAllocation creates transactions based on the bill allocation
func processBillAllocation(s *discordgo.Session, i *discordgo.InteractionCreate, billData *ocr.ExtractBillTextResponse,
	itemAllocations map[int][]string, additionalItemsText string, promptPayID string) (string, error) {

	payeeDiscordID := i.Member.User.ID
	payeeDbID, err := db.GetOrCreateUser(payeeDiscordID)
	if err != nil {
		return "", fmt.Errorf("ไม่สามารถดึงข้อมูลผู้ใช้ได้: %v", err)
	}

	// If promptPayID is not provided, try to get it from the database
	if promptPayID == "" {
		dbPromptPayID, err := db.GetUserPromptPayID(payeeDbID)
		if err == nil {
			promptPayID = dbPromptPayID
		}
	}

	// Validate PromptPay ID if provided
	if promptPayID != "" && !db.IsValidPromptPayID(promptPayID) {
		return "", fmt.Errorf("PromptPay ID ไม่ถูกต้อง: %s", promptPayID)
	}

	// Find all unique users in the allocations
	allUsers := make(map[string]bool)
	for _, users := range itemAllocations {
		if len(users) == 1 && users[0] == "all" {
			// Handle "all" special case later
			continue
		}
		for _, userID := range users {
			allUsers[userID] = true
		}
	}

	// Process additional items
	var additionalItems []struct {
		description string
		amount      float64
		users       []string
	}

	if additionalItemsText != "" {
		lines := strings.Split(additionalItemsText, "\n")
		for _, line := range lines {
			if line = strings.TrimSpace(line); line == "" {
				continue
			}

			// Try to parse the line in the format: "<description> <amount> @user1 @user2..."
			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue // Not enough parts to be valid
			}

			// The last part that's not a user mention should be the amount
			var amount float64
			var description string
			var users []string
			var amountIndex int

			// Find the first user mention in the line
			firstMentionIndex := -1
			for i, part := range parts {
				if userMentionRegex.MatchString(part) {
					firstMentionIndex = i
					break
				}
			}

			// If no user mentions found, skip this line
			if firstMentionIndex == -1 {
				continue
			}

			// Try to parse the part before the user mention as an amount
			amountIndex = firstMentionIndex - 1
			if amountIndex < 0 {
				continue
			}

			amountStr := parts[amountIndex]
			parsedAmount, err := strconv.ParseFloat(amountStr, 64)
			if err != nil {
				continue // The part is not a valid number
			}
			amount = parsedAmount

			// Everything before the amount is the description
			description = strings.Join(parts[:amountIndex], " ")

			// Collect all user mentions
			for i := firstMentionIndex; i < len(parts); i++ {
				if userMentionRegex.MatchString(parts[i]) {
					userID := userMentionRegex.FindStringSubmatch(parts[i])[1]
					users = append(users, userID)
					allUsers[userID] = true
				}
			}

			// If we successfully parsed amount and found users, add to the list
			if amount > 0 && len(users) > 0 {
				additionalItems = append(additionalItems, struct {
					description string
					amount      float64
					users       []string
				}{
					description: description,
					amount:      amount,
					users:       users,
				})
			}
		}
	}

	// Check if there are any users mentioned
	if len(allUsers) == 0 {
		return "", fmt.Errorf("ไม่มีผู้ใช้ถูกระบุในรายการใดๆ")
	}

	// Convert map of users to slice
	var allUsersList []string
	for userID := range allUsers {
		allUsersList = append(allUsersList, userID)
	}

	// Process each bill item and create transactions
	userTotalDebts := make(map[string]float64) // payerDiscordID -> totalOwed
	userTxIDs := make(map[string][]int)        // payerDiscordID -> list of TxIDs for this bill
	var billItemsSummary strings.Builder
	billItemsSummary.WriteString(fmt.Sprintf("**สรุปบิลจากการวิเคราะห์ OCR โดย <@%s>:**\n", payeeDiscordID))
	billItemsSummary.WriteString(fmt.Sprintf("📇 **ร้าน**: %s\n", billData.MerchantName))
	billItemsSummary.WriteString(fmt.Sprintf("📅 **วันที่เวลา**: %s\n\n", billData.Datetime))

	totalBillAmount := 0.0

	// Process bill items
	for idx, item := range billData.Items {
		// Skip items that are not allocated to anyone
		users, allocated := itemAllocations[idx]
		if !allocated || len(users) == 0 {
			continue
		}

		itemTotal := item.Price * float64(item.Quantity)
		totalBillAmount += itemTotal

		// Handle "all" special case
		if len(users) == 1 && users[0] == "all" {
			users = allUsersList
		}

		// Create transaction description
		description := fmt.Sprintf("%s (x%d) จาก %s", item.Name, item.Quantity, billData.MerchantName)

		// Calculate amount per person
		amountPerPerson := itemTotal / float64(len(users))
		if amountPerPerson < 0.01 {
			// Skip very small amounts to avoid dust
			continue
		}

		// Format the item summary
		billItemsSummary.WriteString(fmt.Sprintf("- `%.2f` สำหรับ **%s**, หารกับ: ", itemTotal, description))
		for _, uid := range users {
			billItemsSummary.WriteString(fmt.Sprintf("<@%s> ", uid))
		}
		billItemsSummary.WriteString("\n")

		// Create transaction for each user
		for _, payerDiscordID := range users {
			payerDbID, dbErr := db.GetOrCreateUser(payerDiscordID)
			if dbErr != nil {
				log.Printf("Error DB user %s for item '%s': %v", payerDiscordID, description, dbErr)
				continue // Skip this specific payer for this item
			}

			txID, txErr := db.CreateTransaction(payerDbID, payeeDbID, amountPerPerson, description)
			if txErr != nil {
				log.Printf("Failed to save transaction for user %s, item '%s': %v", payerDiscordID, description, txErr)
				continue // Skip this specific payer for this item
			}

			userTotalDebts[payerDiscordID] += amountPerPerson
			userTxIDs[payerDiscordID] = append(userTxIDs[payerDiscordID], txID)

			// Update user_debts table
			debtErr := db.UpdateUserDebt(payerDbID, payeeDbID, amountPerPerson)
			if debtErr != nil {
				log.Printf("Failed to update debt for user %s, item '%s': %v", payerDiscordID, description, debtErr)
				// Continue anyway as transaction was saved
			}
		}
	}

	// Process additional items
	for _, item := range additionalItems {
		totalBillAmount += item.amount

		// Calculate amount per person
		amountPerPerson := item.amount / float64(len(item.users))
		if amountPerPerson < 0.01 {
			// Skip very small amounts to avoid dust
			continue
		}

		// Format the item summary
		billItemsSummary.WriteString(fmt.Sprintf("- `%.2f` สำหรับ **%s**, หารกับ: ", item.amount, item.description))
		for _, uid := range item.users {
			billItemsSummary.WriteString(fmt.Sprintf("<@%s> ", uid))
		}
		billItemsSummary.WriteString("\n")

		// Create transaction for each user
		for _, payerDiscordID := range item.users {
			payerDbID, dbErr := db.GetOrCreateUser(payerDiscordID)
			if dbErr != nil {
				log.Printf("Error DB user %s for item '%s': %v", payerDiscordID, item.description, dbErr)
				continue // Skip this specific payer for this item
			}

			txID, txErr := db.CreateTransaction(payerDbID, payeeDbID, amountPerPerson, item.description)
			if txErr != nil {
				log.Printf("Failed to save transaction for user %s, item '%s': %v", payerDiscordID, item.description, txErr)
				continue // Skip this specific payer for this item
			}

			userTotalDebts[payerDiscordID] += amountPerPerson
			userTxIDs[payerDiscordID] = append(userTxIDs[payerDiscordID], txID)

			// Update user_debts table
			debtErr := db.UpdateUserDebt(payerDbID, payeeDbID, amountPerPerson)
			if debtErr != nil {
				log.Printf("Failed to update debt for user %s, item '%s': %v", payerDiscordID, item.description, debtErr)
				// Continue anyway as transaction was saved
			}
		}
	}

	// Send bill summary to channel
	s.ChannelMessageSend(i.ChannelID, billItemsSummary.String())

	// Create QR codes for each payer if promptPayID is available
	if len(userTotalDebts) > 0 {
		var qrSummary strings.Builder
		qrSummary.WriteString(fmt.Sprintf("\n**ยอดรวมทั้งสิ้น: %.2f บาท**\n", totalBillAmount))

		// Only mention QR codes if we have a PromptPay ID
		if promptPayID != "" {
			qrSummary.WriteString("\nสร้าง QR Code สำหรับชำระเงิน:\n")
		}
		s.ChannelMessageSend(i.ChannelID, qrSummary.String())

		for payerDiscordID, totalOwed := range userTotalDebts {
			if promptPayID != "" && totalOwed > 0.009 { // Only send QR if ID provided and amount is significant
				relevantTxIDs := userTxIDs[payerDiscordID]
				generateAndSendQrCode(s, i.ChannelID, promptPayID, totalOwed, payerDiscordID,
					fmt.Sprintf("ยอดรวมจากบิล %s โดย <@%s>", billData.MerchantName, payeeDiscordID), relevantTxIDs)
			}
		}
	}

	return "✅ บิลถูกสร้างเรียบร้อยแล้ว! รายละเอียดและ QR Code ได้ถูกส่งไปในช่องสนทนา", nil
}

// downloadFile is a helper function to download files from URLs
func downloadFile(filepath, url string) error {
	return ocr.DownloadFile(filepath, url)
}
