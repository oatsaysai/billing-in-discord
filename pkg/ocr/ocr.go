package ocr

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
	"time"
)

// Client represents an OCR client
type Client struct {
	APIURL string
	APIKey string
}

// ExtractBillTextResponse represents the response from the bill text extraction API
type ExtractBillTextResponse struct {
	Message string `json:"message"`
	Data    struct {
		MerchantName  string     `json:"merchant_name"`
		Datetime      string     `json:"datetime"`
		Items         []BillItem `json:"items"`
		SubTotal      float64    `json:"sub_total"`
		VAT           float64    `json:"vat"`
		ServiceCharge float64    `json:"service_charge"`
		Total         float64    `json:"total"`
	} `json:"data"`
}

// BillItem represents an individual item in a bill
type BillItem struct {
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Quantity int     `json:"quantity"`
}

// NewClient creates a new OCR client
func NewClient(apiURL, apiKey string) *Client {
	return &Client{
		APIURL: apiURL,
		APIKey: apiKey,
	}
}

// DownloadFile downloads a file from a URL
func DownloadFile(filepath, url string) error {
	resp, err := http.Get(url) //nolint:gosec // URL is from Discord CDN, considered safe enough for this context
	if err != nil {
		return fmt.Errorf("http.Get failed for %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s for %s", resp.Status, url)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("os.Create failed for %s: %w", filepath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("io.Copy failed for %s: %w", filepath, err)
	}
	log.Printf("Downloaded file from %s to %s", url, filepath)
	return nil
}

// ExtractBillText extracts text from a bill image and attempts to parse item lines and total
func (c *Client) ExtractBillText(imgPath string) (*ExtractBillTextResponse, error) {
	log.Printf("ExtractBillText called for image %s", imgPath)

	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to read image file: %w", err)
	}
	imgBase64 := base64.StdEncoding.EncodeToString(imgBytes)
	payload := map[string]string{
		"img": fmt.Sprintf("data:image/png;base64,%s", imgBase64),
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to marshal JSON: %w", err)
	}

	// URL for bill text extraction API
	req, err := http.NewRequest("POST", c.APIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}

	// Custom HTTP client to skip TLS verification and set timeout
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 30 * time.Second, // Longer timeout for text extraction
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ExtractBillText: API returned status %d. Body: %s", resp.StatusCode, string(body))
	}

	var result ExtractBillTextResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to unmarshal response: %v, body: %s", err, string(body))
	}
	log.Printf("ExtractBillText successful. Extracted %d items with total %.2f", len(result.Data.Items), result.Data.Total)
	return &result, nil
}
