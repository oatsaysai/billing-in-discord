package verifier

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client represents an Slip Verifier client
type Client struct {
	APIURL string
}

// NewClient creates a new Slip Verifier client
func NewClient(apiURL string) *Client {
	return &Client{
		APIURL: apiURL,
	}
}

// VerifySlipResponse represents the response from the slip verification API
type VerifySlipResponse struct {
	Message string `json:"message"`
	Data    struct {
		Ref          string  `json:"ref"`
		Date         string  `json:"date"`
		SenderBank   string  `json:"sender_bank"`
		SenderName   string  `json:"sender_name"`
		SenderID     string  `json:"sender_id"`
		ReceiverBank string  `json:"receiver_bank"`
		ReceiverName string  `json:"receiver_name"`
		ReceiverID   string  `json:"receiver_id"`
		Amount       float64 `json:"amount"`
	} `json:"data"`
}

// VerifySlip verifies a payment slip image
func (c *Client) VerifySlip(amount float64, imgPath string) (*VerifySlipResponse, error) {
	log.Printf("VerifySlip called for amount %.2f, image %s", amount, imgPath)

	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to read image file: %w", err)
	}
	imgBase64 := base64.StdEncoding.EncodeToString(imgBytes)
	payload := map[string]string{
		"img": fmt.Sprintf("data:image/png;base64,%s", imgBase64), // Assuming PNG, adjust if other formats are common
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("VerifySlip: failed to marshal JSON: %w", err)
	}

	// Fix potential double slash in URL
	baseURL := c.APIURL
	if strings.HasSuffix(baseURL, "/") {
		baseURL = strings.TrimSuffix(baseURL, "/")
	}

	// URL for slip verification API
	url := fmt.Sprintf("%s/%.2f", baseURL, amount)
	log.Printf("VerifySlip using URL: %s", url)

	// Custom HTTP client to skip TLS verification and set timeout
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // WARNING: Insecure, use only if you trust the endpoint or for local dev
		},
		Timeout: 60 * time.Second,
	}

	// Implement retry mechanism
	const maxRetries = 3
	var respBody []byte
	var resp *http.Response

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			return nil, fmt.Errorf("VerifySlip: failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			respBody, err = io.ReadAll(resp.Body)
			if err == nil {
				break // Success - exit the retry loop
			}
			log.Printf("VerifySlip: failed to read response on attempt %d: %v", attempt, err)
		} else {
			log.Printf("VerifySlip: request failed on attempt %d: %v", attempt, err)
		}

		if attempt < maxRetries {
			// Wait before retrying with exponential backoff
			backoffTime := time.Duration(1<<attempt) * time.Second
			log.Printf("VerifySlip: retrying in %v, attempt %d/%d", backoffTime, attempt+1, maxRetries)
			time.Sleep(backoffTime)
		} else {
			// All retries failed
			return nil, fmt.Errorf("VerifySlip: failed to send request after %d attempts: %w", maxRetries, err)
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VerifySlip: API returned status %d. Body: %s", resp.StatusCode, string(respBody))
	}

	var result VerifySlipResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try to log the body if unmarshal fails for debugging
		return nil, fmt.Errorf("VerifySlip: failed to unmarshal response: %v, body: %s", err, string(respBody))
	}
	log.Printf("VerifySlip successful for amount %.2f. API Response Ref: %s", amount, result.Data.Ref)
	return &result, nil
}
