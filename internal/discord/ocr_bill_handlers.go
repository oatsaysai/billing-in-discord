package discord

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oatsaysai/billing-in-discord/pkg/ocr"

	"github.com/bwmarrin/discordgo"
	"github.com/oatsaysai/billing-in-discord/internal/db"
	"github.com/oatsaysai/billing-in-discord/internal/firebase"
	fbclient "github.com/oatsaysai/billing-in-discord/pkg/firebase"
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
		summary.WriteString(fmt.Sprintf("%d. %s x%d = %.2f บาท\n", i+1, item.Name, item.Quantity, item.Price))
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

// Global map to store selected users temporarily
var tempSelectedUsers = make(map[string][]string) // map[messageID][]userID

// Global map to store tokens for web sessions
var webSessionTokens = make(map[string]WebSessionData)

// WebSessionData stores data for a web session
type WebSessionData struct {
	BillData      *ocr.ExtractBillTextResponse
	SelectedUsers []string
	OwnerID       string
	ChannelID     string
	CreatedAt     time.Time
}

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

	// ตรวจสอบว่าผู้ใช้ที่กดปุ่มเป็นคนเริ่มคำสั่งเปิดบิลหรือไม่
	originalMsg, err := s.ChannelMessage(i.ChannelID, messageID)
	if err != nil {
		respondWithError(s, i, "ไม่สามารถตรวจสอบข้อมูลผู้เริ่มคำสั่งได้")
		return
	}

	// ตรวจสอบว่าผู้กดปุ่มเป็นคนเดียวกับคนที่อัปโหลดบิลหรือไม่
	if originalMsg.Author.ID != i.Member.User.ID {
		respondWithError(s, i, "ขออภัย คุณไม่มีสิทธิ์ในการดำเนินการนี้ เฉพาะผู้เริ่มคำสั่งเปิดบิลเท่านั้นที่สามารถระบุรายการได้")
		return
	}

	// Send a message with user select component to select all users who will share this bill
	respondWithUserSelect(s, i, messageID)
}

// respondWithUserSelect sends a user select component to select users
func respondWithUserSelect(s *discordgo.Session, i *discordgo.InteractionCreate, messageID string) {
	// Define min value (needs to be a pointer)
	minV := 1

	// Create user select component
	userSelect := discordgo.SelectMenu{
		CustomID:    fmt.Sprintf("bill_users_select_%s", messageID),
		Placeholder: "เลือกผู้ใช้ทั้งหมดที่ร่วมจ่ายบิลนี้",
		MinValues:   &minV,                    // MinValues is *int
		MaxValues:   25,                       // MaxValues is int in this version
		MenuType:    discordgo.UserSelectMenu, // Use MenuType and the UserSelectMenu constant
		// Options: []discordgo.SelectMenuOption{}, // Not needed for UserSelectMenu
	}

	// Respond with the user select component
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "โปรดเลือกผู้ใช้ทั้งหมดที่ร่วมจ่ายบิลนี้ (รวมตัวคุณเองด้วยถ้าคุณเป็นหนึ่งในผู้ร่วมจ่าย):",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						userSelect,
					},
				},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error sending user select component for messageID %s: %v", messageID, err)
	}
}

// handleUserSelectSubmit handles the user selection submission
func handleUserSelectSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	parts := strings.Split(data.CustomID, "_")
	if len(parts) < 4 {
		respondWithError(s, i, "รูปแบบ custom ID ไม่ถูกต้อง")
		return
	}

	// Extract the message ID that contains the bill data
	messageID := parts[3]

	// Get the bill data from memory
	billData := getBillOCRData(messageID)
	if billData == nil {
		respondWithError(s, i, "ไม่พบข้อมูลบิล หรือข้อมูลหมดอายุแล้ว")
		return
	}

	// Get the selected user IDs
	selectedUserIDs := data.Values

	// Store the selected users in memory for later use
	tempSelectedUsers[messageID] = selectedUserIDs

	// Acknowledge the selection
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ เลือกผู้ใช้ %d คนเรียบร้อยแล้ว กำลังสร้างเว็บไซต์สำหรับแบ่งรายการ...", len(selectedUserIDs)),
			Components: []discordgo.MessageComponent{}, // Remove the user select component
		},
	})

	if err != nil {
		log.Printf("Error acknowledging user selection: %v", err)
		return
	}

	// Create a website on Firebase Hosting for bill allocation
	createBillWebsite(s, i, messageID)
}

// createBillWebsite creates a website on Firebase Hosting for bill allocation
func createBillWebsite(s *discordgo.Session, i *discordgo.InteractionCreate, messageID string) {
	// Get the bill data from memory
	billData := getBillOCRData(messageID)
	if billData == nil {
		sendFollowupError(s, i, "ไม่พบข้อมูลบิล หรือข้อมูลหมดอายุแล้ว")
		return
	}

	// Get the selected users
	selectedUsers, exists := tempSelectedUsers[messageID]
	if !exists || len(selectedUsers) == 0 {
		sendFollowupError(s, i, "ไม่พบข้อมูลผู้ใช้ที่เลือกไว้")
		return
	}

	// Prepare user information for display on the website
	webUsers := make([]map[string]interface{}, 0)
	for _, userID := range selectedUsers {
		user, err := s.User(userID)
		if err != nil {
			log.Printf("Error fetching user %s: %v", userID, err)
			continue
		}

		webUsers = append(webUsers, map[string]interface{}{
			"id":   userID,
			"name": user.Username,
		})
	}

	// Prepare bill items information for display on the website
	webItems := make([]map[string]interface{}, 0)
	for i, item := range billData.Items {
		webItems = append(webItems, map[string]interface{}{
			"id":       i + 1,
			"name":     item.Name,
			"quantity": item.Quantity,
			"price":    item.Price,
			"total":    float64(item.Quantity) * item.Price,
		})
	}

	// Generate a token for the web session
	token := generateToken()

	// Store the session data
	webSessionTokens[token] = WebSessionData{
		BillData:      billData,
		SelectedUsers: selectedUsers,
		OwnerID:       i.Member.User.ID,
		ChannelID:     i.ChannelID,
		CreatedAt:     time.Now(),
	}

	// Get the Firebase client from the discord package
	fbClient := getFirebaseClient()
	if fbClient == nil {
		sendFollowupError(s, i, "Firebase client ไม่ได้ถูกกำหนดค่า")
		return
	}

	// Deploy the website using the Firebase client
	websiteURL, err := firebase.DeployBillWebsite(fbClient, token, billData.MerchantName, webItems, webUsers)
	if err != nil {
		log.Printf("Error deploying bill website: %v", err)
		sendFollowupError(s, i, fmt.Sprintf("เกิดข้อผิดพลาดในการสร้างเว็บไซต์: %v", err))
		return
	}

	// Send the website URL to the user
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: stringPtr(fmt.Sprintf("✅ สร้างเว็บไซต์สำหรับแบ่งรายการเรียบร้อยแล้ว\n\nโปรดเข้าไปที่ลิงก์นี้เพื่อระบุรายการที่แต่ละคนต้องจ่าย:\n%s\n\n⚠️ ลิงก์นี้จะหมดอายุใน 30 นาที", websiteURL)),
	})
}

// generateToken generates a random token for web sessions
func generateToken() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		log.Printf("Error generating random token: %v", err)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.URLEncoding.EncodeToString(b)
}

// getFirebaseClient returns the Firebase client
func getFirebaseClient() *fbclient.Client {
	return firebaseClient
}

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
	return &s
}

// sendFollowupError sends a followup error message
func sendFollowupError(s *discordgo.Session, i *discordgo.InteractionCreate, errorMsg string) {
	// Add proper handling for return values
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: fmt.Sprintf("⚠️ %s", errorMsg),
		Flags:   discordgo.MessageFlagsEphemeral,
	})

	if err != nil {
		log.Printf("Error sending followup error message: %v", err)
	}
}

// HandleBillWebhookCallback processes callbacks from the bill allocation website
func HandleBillWebhookCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		log.Printf("Error reading webhook request body: %v", err)
		return
	}

	// Parse the JSON payload
	var payload struct {
		Token           string                   `json:"token"`
		ItemAllocations map[string][]string      `json:"itemAllocations"` // Key is item index as string
		AdditionalItems []map[string]interface{} `json:"additionalItems"`
		PromptPayID     string                   `json:"promptPayID"`
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		http.Error(w, "Failed to parse JSON payload", http.StatusBadRequest)
		log.Printf("Error parsing webhook JSON payload: %v", err)
		return
	}

	// Verify the token and retrieve session data
	sessionData, exists := webSessionTokens[payload.Token]
	if !exists {
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		log.Printf("Invalid or expired token in webhook: %s", payload.Token)
		return
	}

	// Convert string indices to integers in itemAllocations
	itemAllocations := make(map[int][]string)
	for itemIdxStr, users := range payload.ItemAllocations {
		itemIdx, err := strconv.Atoi(itemIdxStr)
		if err != nil {
			// Skip invalid indices
			log.Printf("Invalid item index in webhook payload: %s", itemIdxStr)
			continue
		}
		itemAllocations[itemIdx] = users
	}

	// Convert additionalItems to the format expected by processBillAllocation
	additionalItemsText := ""
	for _, item := range payload.AdditionalItems {
		if desc, ok := item["description"].(string); ok {
			if amount, ok := item["amount"].(float64); ok {
				if users, ok := item["users"].([]interface{}); ok {
					// Format: "<description> <amount> @user1 @user2..."
					line := fmt.Sprintf("%s %v", desc, amount)
					for _, user := range users {
						if userID, ok := user.(string); ok {
							line += fmt.Sprintf(" <@%s>", userID)
						}
					}
					additionalItemsText += line + "\n"
				}
			}
		}
	}

	// Process the bill allocation asynchronously
	go func() {
		// Create a dummy interaction for processBillAllocation
		dummyInteraction := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				Member: &discordgo.Member{
					User: &discordgo.User{
						ID: sessionData.OwnerID,
					},
				},
				ChannelID: sessionData.ChannelID,
			},
		}

		// Get Discord session
		// Note: In a real implementation, you would need to access the Discord session

		// Process the bill allocation
		successMsg, err := processBillAllocation(session, dummyInteraction, sessionData.BillData, itemAllocations, additionalItemsText, payload.PromptPayID)
		if err != nil {
			log.Printf("Error processing bill allocation from webhook: %v", err)
			// Send error message to Discord channel
			session.ChannelMessageSend(sessionData.ChannelID, fmt.Sprintf("⚠️ เกิดข้อผิดพลาดในการสร้างบิลจากเว็บไซต์: %v", err))
			return
		}

		// Send success message to Discord channel
		session.ChannelMessageSend(sessionData.ChannelID, successMsg)

		// Clean up the data
		delete(billOCRDataStore, strings.Split(payload.Token, "_")[0])
		delete(tempSelectedUsers, strings.Split(payload.Token, "_")[0])
		delete(webSessionTokens, payload.Token)
	}()

	// Respond with success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","message":"Bill allocation is being processed"}`))
}

// processItemAllocations processes the item allocations text and returns a map of item index to user IDs
func processItemAllocations(itemsAllocationText string, allUsers []string, billData *ocr.ExtractBillTextResponse) map[int][]string {
	itemAllocations := make(map[int][]string) // Map of item index to list of user IDs

	// Parse each line of the items allocation text
	if itemsAllocationText != "" {
		lines := strings.Split(itemsAllocationText, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || !strings.ContainsAny(line, "123456789") {
				continue // Skip empty lines or lines without numbers
			}

			// Try to extract the item number from the beginning of the line
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}

			// First part should be the item number
			itemNumStr := parts[0]
			itemNum, err := strconv.Atoi(itemNumStr)
			if err != nil {
				continue // Not a valid item number
			}

			// Adjust item number to 0-based index
			itemIdx := itemNum - 1
			if itemIdx < 0 || itemIdx >= len(billData.Items) {
				continue // Invalid item index
			}

			// Check if there are user mentions in the line
			mentions := userMentionRegex.FindAllStringSubmatch(line, -1)
			if len(mentions) > 0 {
				// Add the mentioned users to the item
				var userIDs []string
				for _, mention := range mentions {
					userIDs = append(userIDs, mention[1])
				}
				itemAllocations[itemIdx] = userIDs
			} else if strings.Contains(strings.ToLower(line), "all") {
				// If the line contains "all", allocate to all users
				itemAllocations[itemIdx] = []string{"all"}
			}
		}
	}

	return itemAllocations
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
