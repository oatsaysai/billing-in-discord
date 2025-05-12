package ocr

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client represents an OCR client
type Client struct {
	APIURL string
	APIKey string
}

// ExtractBillTextResponse represents the response from the bill text extraction API
type ExtractBillTextResponse struct {
	MerchantName  string     `json:"merchant_name"`
	Datetime      string     `json:"datetime"`
	Items         []BillItem `json:"items"`
	SubTotal      float64    `json:"sub_total"`
	VAT           float64    `json:"vat"`
	ServiceCharge float64    `json:"service_charge"`
	Total         float64    `json:"total"`
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

	// Open the file
	file, err := os.Open(imgPath)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to open image file: %w", err)
	}
	defer file.Close()

	// Create a new multipart writer
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Create form file
	part, err := writer.CreateFormFile("image", filepath.Base(imgPath))
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to create form file: %w", err)
	}

	// Copy the file data to the form
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to copy image to form: %w", err)
	}

	// Close the multipart writer
	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to close multipart writer: %w", err)
	}

	// Create the request
	req, err := http.NewRequest("POST", c.APIURL, body)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to create request: %w", err)
	}

	// Set the content type to multipart/form-data
	req.Header.Set("Content-Type", writer.FormDataContentType())
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ExtractBillText: API returned status %d. Body: %s", resp.StatusCode, string(respBody))
	}

	var result ExtractBillTextResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("ExtractBillText: failed to unmarshal response: %v, body: %s", err, string(respBody))
	}

	log.Printf("ExtractBillText successful. Extracted %d items with total %.2f", len(result.Items), result.Total)
	return &result, nil
}
