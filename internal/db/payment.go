package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// Define regex patterns for transaction ID extraction
var (
	txIDRegex  = regexp.MustCompile(`(?:TxID|Tx ID):\s*(\d+)`)
	txIDsRegex = regexp.MustCompile(`(?:TxIDs|Tx IDs):\s*([\d,\s]+)`)
)

// FindIntendedPayee attempts to determine the intended payee for a payment
// based on the debtor and amount. It returns the payee's Discord ID if found,
// or an error if the payee cannot be determined or if multiple possibilities exist.
func FindIntendedPayee(debtorDiscordID string, amount float64) (string, error) {
	debtorDbID, err := GetOrCreateUser(debtorDiscordID)
	if err != nil {
		return "", fmt.Errorf("ไม่พบผู้จ่ายเงิน %s ใน DB: %w", debtorDiscordID, err)
	}

	var payeeDiscordID string
	var count int // To check if query returns exactly one row
	// First, check if there's a single creditor to whom this debtor owes this exact total amount
	query := `
		SELECT u.discord_id, COUNT(*) OVER() as total_matches
		FROM user_debts ud
		JOIN users u ON ud.creditor_id = u.id
		WHERE ud.debtor_id = $1
		  AND ABS(ud.amount - $2::numeric) < 0.01 -- Amount matches total debt closely
		  AND ud.amount > 0.009 -- Debt is significant
		LIMIT 1; -- Only interested if there's one unique match
	`
	err = Pool.QueryRow(context.Background(), query, debtorDbID, amount).Scan(&payeeDiscordID, &count)
	if err == nil && count == 1 {
		log.Printf("findIntendedPayee: Found single matching creditor %s based on total debt amount %.2f for debtor %s", payeeDiscordID, amount, debtorDiscordID)
		return payeeDiscordID, nil
	}
	if err == nil && count > 1 {
		log.Printf("findIntendedPayee: Ambiguous - Debtor %s owes %.2f to multiple creditors based on total debt amount.", debtorDiscordID, amount)
		// Continue to check individual transactions
	}

	// If not, check for a single unpaid transaction of this amount from this debtor
	query = `
		SELECT u.discord_id, COUNT(*) OVER() as payee_count
		FROM transactions t
		JOIN users u ON t.payee_id = u.id
		WHERE t.payer_id = $1
		  AND ABS(t.amount - $2::numeric) < 0.01 -- Transaction amount matches closely
		  AND t.already_paid = false
		GROUP BY u.discord_id -- Group by payee in case of multiple tx to same payee
		LIMIT 2; -- Fetch up to 2 to detect ambiguity
	`
	rows, err := Pool.Query(context.Background(), query, debtorDbID, amount)
	if err != nil {
		log.Printf("findIntendedPayee: Error querying transactions for debtor %s amount %.2f: %v", debtorDiscordID, amount, err)
		return "", fmt.Errorf("เกิดข้อผิดพลาดในการค้นหาผู้รับเงิน")
	}
	defer rows.Close()

	var potentialPayees []string
	for rows.Next() {
		var payee string
		var payeeCount int                                     // This will be total distinct payees from the GROUP BY
		if err := rows.Scan(&payee, &payeeCount); err != nil { // payeeCount here is not what we expect from OVER()
			log.Printf("findIntendedPayee: Error scanning transaction row: %v", err)
			continue
		}
		potentialPayees = append(potentialPayees, payee)
	}

	if len(potentialPayees) == 1 {
		log.Printf("findIntendedPayee: Found single matching payee %s based on transaction amount %.2f for debtor %s", potentialPayees[0], amount, debtorDiscordID)
		return potentialPayees[0], nil
	}

	if len(potentialPayees) > 1 {
		log.Printf("findIntendedPayee: Ambiguous - Found multiple potential payees (%v) based on transaction amount %.2f for debtor %s", potentialPayees, amount, debtorDiscordID)
		return "", fmt.Errorf("พบผู้รับเงินที่เป็นไปได้หลายคนสำหรับจำนวนเงินนี้ โปรดใช้คำสั่ง `!paid <TxID>` โดยผู้รับเงิน")
	}

	log.Printf("findIntendedPayee: Could not determine unique intended payee for debtor %s, amount %.2f", debtorDiscordID, amount)
	return "", fmt.Errorf("ไม่สามารถระบุผู้รับเงินที่แน่นอนสำหรับยอดนี้ได้ โปรดให้ผู้รับเงินยืนยันด้วย `!paid <TxID>` หรือตอบกลับ QR ที่มี TxID")
}

// ReduceDebtFromPayment reduces debt for a payment between users
func ReduceDebtFromPayment(debtorDiscordID, payeeDiscordID string, amount float64) error {
	debtorDbID, err := GetOrCreateUser(debtorDiscordID)
	if err != nil {
		return fmt.Errorf("ไม่พบผู้จ่ายเงิน %s ใน DB: %w", debtorDiscordID, err)
	}
	payeeDbID, err := GetOrCreateUser(payeeDiscordID)
	if err != nil {
		return fmt.Errorf("ไม่พบผู้รับเงิน %s ใน DB: %w", payeeDiscordID, err)
	}

	tx, err := Pool.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("ไม่สามารถเริ่ม Transaction ได้: %w", err)
	}
	defer tx.Rollback(context.Background()) // Rollback if commit isn't called

	result, err := tx.Exec(context.Background(),
		`UPDATE user_debts SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
         WHERE debtor_id = $2 AND creditor_id = $3 AND amount > 0.009`, // only update if there's existing debt
		amount, debtorDbID, payeeDbID)

	if err != nil {
		return fmt.Errorf("เกิดข้อผิดพลาดขณะอัปเดตหนี้สินรวม: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// This could mean the debt was already 0, or became < 0 due to overpayment.
		// If it became <0 and we want to zero it out, we could do another update.
		// For now, just log. If debt was paid, `user_debts.amount` would be <= 0.
		log.Printf("Debt reduction update affected 0 rows for debtor %d paying creditor %d amount %.2f. Debt might be zero or negative already, or there was no debt record.", debtorDbID, payeeDbID, amount)
		// Optionally, ensure it doesn't go negative or create a debt if none existed (though should not happen with `amount > 0.009` guard)
		// One strategy could be to set to 0 if amount - $1 < 0
		zeroResult, errZero := tx.Exec(context.Background(),
			`UPDATE user_debts SET amount = 0, updated_at = CURRENT_TIMESTAMP
		   WHERE debtor_id = $1 AND creditor_id = $2 AND amount > 0.009 AND amount < $3`, // Only zero if original amount was less than payment
			debtorDbID, payeeDbID, amount)
		if errZero != nil {
			log.Printf("Warning/Error trying to zero out remaining debt for debtor %d creditor %d amount %.2f: %v", debtorDbID, payeeDbID, amount, errZero)
		} else if zeroResult.RowsAffected() > 0 {
			log.Printf("Zeroed out remaining debt for debtor %d paying creditor %d (Payment %.2f)", debtorDbID, payeeDbID, amount)
		}
	}

	if err = tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("ไม่สามารถ Commit Transaction ได้: %w", err)
	}

	log.Printf("General debt reduction successful: Debtor %d, Creditor %d, Amount %.2f", debtorDbID, payeeDbID, amount)
	return nil
}

// GetPayeeDbIDFromTx gets the payee database ID from a transaction
func GetPayeeDbIDFromTx(txID int) (int, error) {
	var payeeDbID int
	query := `SELECT payee_id FROM transactions WHERE id = $1`
	err := Pool.QueryRow(context.Background(), query, txID).Scan(&payeeDbID)
	if err != nil {
		log.Printf("Error fetching payee DB ID for TxID %d: %v", txID, err)
		return 0, err
	}
	return payeeDbID, nil
}

// GetUnpaidTransactionIDsAndDetails gets unpaid transaction IDs and details between users
func GetUnpaidTransactionIDsAndDetails(debtorDbID, creditorDbID int, detailLimit int) ([]int, string, float64, error) {
	query := `
        SELECT id, amount, description
        FROM transactions
        WHERE payer_id = $1 AND payee_id = $2 AND already_paid = false
        ORDER BY created_at ASC;
    `
	rows, err := Pool.Query(context.Background(), query, debtorDbID, creditorDbID)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()

	var details strings.Builder
	var txIDs []int
	var totalAmount float64
	count := 0
	for rows.Next() {
		var id int
		var amount float64
		var description sql.NullString
		if err := rows.Scan(&id, &amount, &description); err != nil {
			return nil, "", 0, err
		}
		descText := description.String
		if !description.Valid || descText == "" {
			descText = "(ไม่มีรายละเอียด)"
		}
		if detailLimit <= 0 || count < detailLimit { // if detailLimit is 0 or less, show all
			details.WriteString(fmt.Sprintf("- `%.2f` บาท: %s (TxID: %d)\n", amount, descText, id))
		} else if count == detailLimit {
			details.WriteString("- ... (และรายการอื่นๆ)\n")
		}
		txIDs = append(txIDs, id)
		totalAmount += amount
		count++
	}
	if count == 0 {
		return nil, "", 0, nil // No unpaid transactions found
	}
	return txIDs, details.String(), totalAmount, nil
}

// ParseBotQRMessageContent parses the content of a QR message sent by the bot
func ParseBotQRMessageContent(content string) (debtorDiscordID string, amount float64, txIDs []int, err error) {
	// Regular expression to capture debtor ID and amount
	re := regexp.MustCompile(`<@!?(\d+)> กรุณาชำระ ([\d.]+) บาท`)
	matches := re.FindStringSubmatch(content)
	if len(matches) < 3 {
		return "", 0, nil, fmt.Errorf("เนื้อหาข้อความไม่ตรงกับรูปแบบข้อความ QR ของบอท (ไม่พบ debtor/amount)")
	}

	debtorDiscordID = matches[1]
	parsedAmount, parseErr := strconv.ParseFloat(matches[2], 64)
	if parseErr != nil {
		return "", 0, nil, fmt.Errorf("ไม่สามารถแยกวิเคราะห์จำนวนเงินจากข้อความ QR ของบอท: %v", parseErr)
	}
	amount = parsedAmount

	// Try to parse multiple TxIDs: (TxIDs: 1,2,3)
	txsMatch := txIDsRegex.FindStringSubmatch(content)
	if len(txsMatch) == 2 { // txsMatch[0] is full match, txsMatch[1] is the capture group "1,2,3"
		idStrings := strings.Split(txsMatch[1], ",")
		txIDs = make([]int, 0, len(idStrings))
		for _, idStr := range idStrings {
			trimmedIDStr := strings.TrimSpace(idStr)
			if parsedTxID, txErr := strconv.Atoi(trimmedIDStr); txErr == nil {
				txIDs = append(txIDs, parsedTxID)
			} else {
				log.Printf("Warning: Failed to parse TxID '%s' from multi-ID list: %v", trimmedIDStr, txErr)
				// Potentially return error here if strict parsing is needed
			}
		}
		if len(txIDs) > 0 {
			return debtorDiscordID, amount, txIDs, nil
		}
		// If parsing failed for all, fall through to single TxID or no TxID
	}

	// Try to parse single TxID: (TxID: 123)
	txMatch := txIDRegex.FindStringSubmatch(content)
	if len(txMatch) == 2 { // txMatch[0] is full match, txMatch[1] is the capture group "123"
		if parsedTxID, txErr := strconv.Atoi(txMatch[1]); txErr == nil {
			txIDs = []int{parsedTxID} // Return as a slice with one element
			return debtorDiscordID, amount, txIDs, nil
		} else {
			log.Printf("Warning: Failed to parse single TxID '%s': %v", txMatch[1], txErr)
			// Potentially return error here
		}
	}

	// If no TxID regex matched, or parsing failed, return with nil txIDs
	return debtorDiscordID, amount, nil, nil
}

// MarkTransactionPaidAndUpdateDebt marks a transaction as paid and updates the debt
func MarkTransactionPaidAndUpdateDebt(txID int) error {
	var payerDbID, payeeDbID int
	var amount float64

	// Begin a database transaction
	tx, err := Pool.Begin(context.Background())
	if err != nil {
		return fmt.Errorf("failed to begin database transaction: %w", err)
	}
	defer tx.Rollback(context.Background()) // Ensure rollback if not committed

	// Retrieve transaction details and lock the row for update
	err = tx.QueryRow(context.Background(),
		`SELECT payer_id, payee_id, amount FROM transactions WHERE id = $1 AND already_paid = false FOR UPDATE`, txID,
	).Scan(&payerDbID, &payeeDbID, &amount)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows in result set") {
			log.Printf("TxID %d already paid or does not exist.", txID)
			// This is not an error for the caller if the goal is to ensure it's paid
			return fmt.Errorf("TxID %d ไม่พบ หรือถูกชำระไปแล้ว", txID) // Return specific error for !paid command
		}
		return fmt.Errorf("failed to retrieve unpaid transaction %d: %w", txID, err)
	}

	// Mark the transaction as paid
	_, err = tx.Exec(context.Background(), `UPDATE transactions SET already_paid = TRUE, paid_at = CURRENT_TIMESTAMP WHERE id = $1`, txID)
	if err != nil {
		return fmt.Errorf("failed to mark transaction %d as paid: %w", txID, err)
	}

	// Update the corresponding user_debts record by subtracting the amount
	// Note: This relies on updateUserDebt which uses ON CONFLICT to add, so we need direct subtraction here.
	_, err = tx.Exec(context.Background(),
		`UPDATE user_debts SET amount = amount - $1, updated_at = CURRENT_TIMESTAMP
	WHERE debtor_id = $2 AND creditor_id = $3`,
		amount, payerDbID, payeeDbID)
	if err != nil {
		// Log error but don't necessarily fail the whole operation if transaction was marked paid
		// This could happen if the user_debts record was already cleared or inconsistent.
		log.Printf("Warning/Error updating user_debts for txID %d (debtor %d, creditor %d, amount %.2f): %v. This might be okay if debt was already cleared or manually adjusted.", txID, payerDbID, payeeDbID, amount, err)
	}

	// Commit the database transaction
	if err = tx.Commit(context.Background()); err != nil {
		return fmt.Errorf("failed to commit database transaction for txID %d: %w", txID, err)
	}
	log.Printf("Transaction ID %d marked as paid and debts updated.", txID)
	return nil
}
