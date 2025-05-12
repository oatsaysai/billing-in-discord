package qrcode

import (
	"fmt"
	"os"
	"time"

	pp "github.com/Frontware/promptpay"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
)

// Generate creates a QR code for PromptPay payment
func Generate(promptPayID string, amount float64) (string, error) {
	payment := pp.PromptPay{PromptPayID: promptPayID, Amount: amount}
	qrcodeStr, err := payment.Gen()
	if err != nil {
		return "", fmt.Errorf("error generating PromptPay data: %w", err)
	}

	qrc, err := qrcode.New(qrcodeStr)
	if err != nil {
		return "", fmt.Errorf("error creating QR code: %w", err)
	}

	// Generate a unique filename
	filename := fmt.Sprintf("qr_%s_%d.jpg", promptPayID, time.Now().UnixNano())
	fileWriter, err := standard.New(filename)
	if err != nil {
		return "", fmt.Errorf("error creating file writer: %w", err)
	}

	if err = qrc.Save(fileWriter); err != nil {
		os.Remove(filename) // Clean up on error
		return "", fmt.Errorf("error saving QR code: %w", err)
	}

	return filename, nil
}

// Remove deletes the QR code file
func Remove(filename string) error {
	return os.Remove(filename)
}